//go:build e2e || sandbox

// Package e2e_test runs the real PayGate binary against real Postgres, Redis and RabbitMQ
// (test/e2e/docker-compose.yml) and drives it over HTTP the way a calling service would.
// The gateway is chosen by build tag: `e2e` uses a fake Midtrans (make e2e), `sandbox`
// the real Midtrans sandbox with a person paying in its simulators (make sandbox).
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"

	"github.com/Trisentosa/payment-module/internal/pkg/apikey"
)

const (
	adminKey = "e2e-admin-key-0123456789-0123456789"
	orderKey = "pgk_e2e-order-service"
	billKey  = "pgk_e2e-billing-service"
)

var (
	paygate string // base URL of the running server
	events  *eventLog
	db      *pgx.Conn
)

// gatewaySetup is what a build tag's startGateway provides: the server's Midtrans settings,
// a cleanup, and an optional check once the server is up.
type gatewaySetup struct {
	env        []string
	stop       func()
	afterStart func() error
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e setup:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	ctx := context.Background()
	root, err := filepath.Abs("../..")
	if err != nil {
		return 0, err
	}
	work, err := os.MkdirTemp("", "paygate-e2e-")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(work) }()

	dbHost, dbPort := envOr("E2E_DB_HOST", "127.0.0.1"), envOr("E2E_DB_PORT", "55433")
	redisAddr := envOr("E2E_REDIS_ADDR", "127.0.0.1:56379")
	amqpURL := envOr("E2E_RABBITMQ_URL", "amqp://guest:guest@127.0.0.1:55672/paygate")

	// Start from empty stores so runs don't see each other's payments.
	dsn := fmt.Sprintf("postgres://paygate_user:secret@%s:%s/paygate_e2e?sslmode=disable", dbHost, dbPort)
	if db, err = pgx.Connect(ctx, dsn); err != nil {
		return 0, fmt.Errorf("postgres (is `make e2e-up` running?): %w", err)
	}
	defer func() { _ = db.Close(ctx) }()
	if _, err = db.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		return 0, fmt.Errorf("reset schema: %w", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err = rdb.FlushDB(ctx).Err(); err != nil {
		return 0, fmt.Errorf("redis: %w", err)
	}
	_ = rdb.Close()

	port, err := serverPort()
	if err != nil {
		return 0, err
	}
	metricsPort, err := freePort()
	if err != nil {
		return 0, err
	}
	paygate = fmt.Sprintf("http://127.0.0.1:%d", port)
	gw, err := startGateway(root, paygate)
	if err != nil {
		return 0, fmt.Errorf("gateway: %w", err)
	}
	defer gw.stop()

	env := append(os.Environ(),
		"SERVER_PORT="+fmt.Sprint(port), "METRICS_PORT="+fmt.Sprint(metricsPort),
		"DB_HOST="+dbHost, "DB_PORT="+dbPort, "DB_NAME=paygate_e2e", "DB_USER=paygate_user", "DB_PASSWORD=secret",
		"REDIS_ADDR="+redisAddr, "REDIS_PASSWORD=",
		"RABBITMQ_URL="+amqpURL,
		"ADMIN_API_KEY="+adminKey,
		"CALLER_API_KEYS=order-service:"+apikey.Hash(orderKey)+",billing-service:"+apikey.Hash(billKey),
		// Reconcile within seconds instead of minutes.
		"RECONCILE_INTERVAL=1s", "RECONCILE_REFUND_GRACE=2s", "RECONCILE_REFUND_RETRY_AFTER=4s",
		"LOG_FORMAT=json", "OTEL_EXPORTER_OTLP_ENDPOINT=",
	)
	env = append(env, gw.env...) // later entries win

	if out, err := goCmd(root, env, "run", "./cmd/migrate", "up"); err != nil {
		return 0, fmt.Errorf("migrate: %w\n%s", err, out)
	}
	bin := filepath.Join(work, "paygate")
	if out, err := goCmd(root, env, "build", "-o", bin, "./cmd/server"); err != nil {
		return 0, fmt.Errorf("build server: %w\n%s", err, out)
	}

	// Listen before the server starts, so no event is missed.
	if events, err = listen(amqpURL); err != nil {
		return 0, fmt.Errorf("rabbitmq: %w", err)
	}
	defer events.Close()

	logPath := filepath.Join(work, "paygate.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return 0, err
	}
	server := exec.Command(bin)
	server.Env, server.Stdout, server.Stderr = env, logFile, logFile
	if err = server.Start(); err != nil {
		return 0, fmt.Errorf("start server: %w", err)
	}
	defer func() {
		_ = server.Process.Signal(os.Interrupt)
		_ = server.Wait()
		_ = logFile.Close()
	}()
	if err = waitHealthy(paygate+"/healthz", 30*time.Second); err != nil {
		return 0, fmt.Errorf("%w\nserver log:\n%s", err, tail(logPath))
	}
	if gw.afterStart != nil {
		if err = gw.afterStart(); err != nil {
			return 0, err
		}
	}

	code := m.Run()
	if code != 0 {
		fmt.Fprintf(os.Stderr, "\n--- PayGate server log (last 60 lines) ---\n%s\n", tail(logPath))
	}
	return code, nil
}

func goCmd(dir string, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir, cmd.Env = dir, env
	return cmd.CombinedOutput()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitHealthy(url string, within time.Duration) error {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if res, err := http.Get(url); err == nil {
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("server not healthy after %s", within)
}

func tail(path string) string {
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > 60 {
		lines = lines[len(lines)-60:]
	}
	return strings.Join(lines, "\n")
}

// ---- HTTP client ----

type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body as an object.
func (r response) JSON(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("decode %d response %q: %v", r.Status, r.Body, err)
	}
	return m
}

// call sends a request as the caller with apiKey ("" for none). Extra headers come in pairs.
func call(t *testing.T, method, path, apiKey string, body any, headers ...string) response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, paygate+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return response{Status: res.StatusCode, Header: res.Header, Body: b}
}

func admin(t *testing.T, method, path string) response {
	t.Helper()
	return call(t, method, path, "", nil, "X-Admin-Key", adminKey)
}

// expect fails the test unless r has the wanted status.
func expect(t *testing.T, r response, want int, what string) response {
	t.Helper()
	if r.Status != want {
		t.Fatalf("%s: status %d, want %d; body %s", what, r.Status, want, r.Body)
	}
	return r
}

// get reads a nested field such as "next_action.type" from a decoded object.
func get(m map[string]any, path string) any {
	var cur any = m
	for _, k := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[k]
	}
	return cur
}

func str(m map[string]any, path string) string {
	s, _ := get(m, path).(string)
	return s
}

func num(m map[string]any, path string) int64 {
	f, _ := get(m, path).(float64)
	return int64(f)
}

// eventually retries check until it passes or within runs out, then fails with its last reason.
func eventually(t *testing.T, within time.Duration, what string, check func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		ok, why := check()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s: %s", what, within, why)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// paymentStatus reads the payment as its owner.
func paymentStatus(t *testing.T, key, id string) string {
	t.Helper()
	return str(expect(t, call(t, http.MethodGet, "/payments/"+id, key, nil), http.StatusOK, "get payment").JSON(t), "status")
}

func waitForStatus(t *testing.T, key, id, want string, within time.Duration) {
	t.Helper()
	eventually(t, within, "payment "+id+" to become "+want, func() (bool, string) {
		got := paymentStatus(t, key, id)
		return got == want, "status is " + got
	})
}

// ---- payments ----

// runID keeps reference_ids unique if the suite runs more than once against one database.
var runID = time.Now().Format("150405")

func ref(name string) string { return name + "-" + runID }

func paymentRequest(reference, method string, amount int64, extra map[string]any) map[string]any {
	req := map[string]any{
		"reference_id":        reference,
		"amount":              amount,
		"currency":            "IDR",
		"gateway_type":        "MIDTRANS",
		"payment_method_type": method,
		"customer_name":       "Siti",
		"customer_email":      "siti@example.com",
	}
	for k, v := range extra {
		req[k] = v
	}
	return req
}

// create makes a payment as order-service and returns the decoded 201 response.
func create(t *testing.T, req map[string]any) map[string]any {
	t.Helper()
	return expect(t, call(t, http.MethodPost, "/payments", orderKey, req), http.StatusCreated, "create payment").JSON(t)
}

// ---- RabbitMQ ----

type envelope struct {
	EventID       string         `json:"event_id"`
	EventType     string         `json:"event_type"`
	AggregateID   string         `json:"aggregate_id"`
	AggregateType string         `json:"aggregate_type"`
	Version       int            `json:"version"`
	OccurredAt    time.Time      `json:"occurred_at"`
	Source        string         `json:"source_service"`
	Data          map[string]any `json:"data"`
	MessageID     string         `json:"-"` // AMQP message_id
	RoutingKey    string         `json:"-"`
}

// eventLog records every message PayGate publishes to the paygate.payments exchange.
type eventLog struct {
	conn *amqp.Connection
	mu   sync.Mutex
	all  []envelope
}

func listen(url string) (*eventLog, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	// Same declaration as the publisher, so whichever runs first creates the exchange.
	if err = ch.ExchangeDeclare("paygate.payments", "topic", true, false, false, false, nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	q, err := ch.QueueDeclare("", false, true, true, false, nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err = ch.QueueBind(q.Name, "#", "paygate.payments", false, nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	deliveries, err := ch.Consume(q.Name, "e2e", true, true, false, false, nil)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	l := &eventLog{conn: conn}
	go func() {
		for d := range deliveries {
			var e envelope
			if json.Unmarshal(d.Body, &e) != nil {
				continue
			}
			e.MessageID, e.RoutingKey = d.MessageId, d.RoutingKey
			l.mu.Lock()
			l.all = append(l.all, e)
			l.mu.Unlock()
		}
	}()
	return l, nil
}

func (l *eventLog) Close() { _ = l.conn.Close() }

// For returns the events received for one payment, in arrival order.
func (l *eventLog) For(paymentID string) []envelope {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []envelope
	for _, e := range l.all {
		if e.AggregateID == paymentID {
			out = append(out, e)
		}
	}
	return out
}

func (l *eventLog) count(paymentID, routingKey string) int {
	n := 0
	for _, e := range l.For(paymentID) {
		if e.RoutingKey == routingKey {
			n++
		}
	}
	return n
}

// WaitFor waits for an event with routingKey about the payment and returns the first one.
// The outbox worker publishes every 5 s, so allow for a couple of passes.
func (l *eventLog) WaitFor(t *testing.T, paymentID, routingKey string) envelope {
	t.Helper()
	var found envelope
	eventually(t, 20*time.Second, routingKey+" event for "+paymentID, func() (bool, string) {
		for _, e := range l.For(paymentID) {
			if e.RoutingKey == routingKey {
				found = e
				return true, ""
			}
		}
		var seen []string
		for _, e := range l.For(paymentID) {
			seen = append(seen, e.RoutingKey)
		}
		return false, fmt.Sprintf("received %v", seen)
	})
	return found
}
