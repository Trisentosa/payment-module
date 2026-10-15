//go:build sandbox

package e2e_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const sandboxAPI = "https://api.sandbox.midtrans.com"

var (
	sandboxServerKey string
	sandboxClientKey string
	// webhooks is false when no public URL was set up; tests that need Midtrans's
	// notifications are then skipped.
	webhooks bool
)

func serverPort() (int, error) {
	return strconv.Atoi(envOr("SANDBOX_PORT", "18080"))
}

// startGateway points PayGate at the Midtrans sandbox, with notifications routed to a
// public URL: SANDBOX_PUBLIC_URL if set, otherwise a cloudflared quick tunnel.
func startGateway(root, paygateURL string) (gatewaySetup, error) {
	if err := loadDotenv(envOr("SANDBOX_ENV_FILE", filepath.Join(root, ".env"))); err != nil {
		return gatewaySetup{}, err
	}
	sandboxServerKey, sandboxClientKey = os.Getenv("MIDTRANS_SERVER_KEY"), os.Getenv("MIDTRANS_CLIENT_KEY")
	// Refuse production keys: this suite creates and refunds transactions.
	if !strings.HasPrefix(sandboxServerKey, "SB-Mid-server-") || !strings.HasPrefix(sandboxClientKey, "SB-Mid-client-") {
		return gatewaySetup{}, fmt.Errorf("MIDTRANS_SERVER_KEY and MIDTRANS_CLIENT_KEY must be sandbox keys (SB-Mid-server-…, SB-Mid-client-…); set them in .env or SANDBOX_ENV_FILE")
	}

	setup := gatewaySetup{
		env: []string{
			"MIDTRANS_SERVER_KEY=" + sandboxServerKey,
			"MIDTRANS_ENV=sandbox", "MIDTRANS_BASE_URL=",
			"MIDTRANS_TIMEOUT_MS=30000", "RECONCILE_CHARGE_GRACE=2m",
		},
		stop: func() {},
	}

	public := os.Getenv("SANDBOX_PUBLIC_URL")
	if public == "" && os.Getenv("SANDBOX_NO_WEBHOOKS") == "" {
		tunnel, err := startTunnel(paygateURL)
		if err != nil {
			return gatewaySetup{}, err
		}
		public, setup.stop = tunnel.url, tunnel.stop
	}
	if public == "" {
		fmt.Println("SANDBOX_NO_WEBHOOKS set: Midtrans can't reach PayGate; tests that wait for a payment are skipped.")
		return setup, nil
	}

	webhooks = true
	public = strings.TrimRight(public, "/")
	setup.env = append(setup.env, "MIDTRANS_NOTIFICATION_URL="+public+"/webhooks/midtrans")
	setup.afterStart = func() error {
		// A fresh quick tunnel can take a few seconds to resolve.
		if err := waitHealthy(public+"/healthz", 60*time.Second); err != nil {
			return fmt.Errorf("PayGate isn't reachable through %s: %w", public, err)
		}
		fmt.Printf("Midtrans notifications go to %s/webhooks/midtrans\n", public)
		return nil
	}
	return setup, nil
}

type tunnel struct {
	url  string
	stop func()
}

var quickTunnelURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// startTunnel opens a cloudflared quick tunnel (no account needed) to PayGate.
func startTunnel(paygateURL string) (tunnel, error) {
	bin, err := exec.LookPath("cloudflared")
	if err != nil {
		return tunnel{}, fmt.Errorf("Midtrans must reach PayGate's webhook: install cloudflared, or run your own tunnel to %s and set SANDBOX_PUBLIC_URL (SANDBOX_NO_WEBHOOKS=1 runs only the tests that don't need it)", paygateURL)
	}
	cmd := exec.Command(bin, "tunnel", "--no-autoupdate", "--url", paygateURL)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return tunnel{}, err
	}
	if err = cmd.Start(); err != nil {
		return tunnel{}, err
	}
	stop := func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }

	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if u := quickTunnelURL.FindString(sc.Text()); u != "" {
				select {
				case found <- u:
				default:
				}
			}
		}
	}()
	select {
	case u := <-found:
		return tunnel{url: u, stop: stop}, nil
	case <-time.After(45 * time.Second):
		stop()
		return tunnel{}, fmt.Errorf("cloudflared printed no tunnel URL within 45s")
	}
}

// loadDotenv sets KEY=VALUE lines from path that aren't already in the environment.
func loadDotenv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			_ = os.Setenv(k, v)
		}
	}
	return sc.Err()
}

// ---- direct Midtrans calls, to check PayGate against Midtrans's own view ----

func sandboxGet(path string, auth bool) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, sandboxAPI+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if auth {
		req.SetBasicAuth(sandboxServerKey, "")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("HTTP %d %s", res.StatusCode, b)
	}
	return m, nil
}

// midtransStatus is Midtrans's GET /v2/{order_id}/status for a payment.
func midtransStatus(paymentID string) (map[string]any, error) {
	return sandboxGet("/v2/"+paymentID+"/status", true)
}

// cardToken tokenizes a test card the way Midtrans.js does in a real checkout.
func cardToken(number string) (string, error) {
	q := url.Values{
		"client_key":     {sandboxClientKey},
		"card_number":    {strings.ReplaceAll(number, " ", "")},
		"card_exp_month": {"12"},
		"card_exp_year":  {fmt.Sprint(time.Now().Year() + 3)},
		"card_cvv":       {"123"},
	}
	m, err := sandboxGet("/v2/token?"+q.Encode(), false)
	if err != nil {
		return "", err
	}
	tok, _ := m["token_id"].(string)
	if tok == "" {
		return "", fmt.Errorf("no token_id: %v", m)
	}
	return tok, nil
}
