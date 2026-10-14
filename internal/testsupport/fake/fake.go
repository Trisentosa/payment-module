// Package fake provides in-memory implementations of PayGate's ports for unit tests.
// Nothing here is wired into the running service.
package fake

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// PaymentRepo is an in-memory payment.Repository. Like the Postgres repo, Save rejects a
// second payment with the same (reference_id, caller_service) and consumes pending events.
type PaymentRepo struct {
	mu        sync.Mutex
	byID      map[uuid.UUID]*payment.Payment
	Events    []payment.DomainEvent // every event persisted by Save, in order
	SaveCalls int
}

func NewPaymentRepo(seed ...*payment.Payment) *PaymentRepo {
	r := &PaymentRepo{byID: map[uuid.UUID]*payment.Payment{}}
	for _, p := range seed {
		r.byID[p.ID] = p
	}
	return r
}

func (r *PaymentRepo) Save(_ context.Context, p *payment.Payment) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.SaveCalls++
	if _, known := r.byID[p.ID]; !known {
		for _, other := range r.byID {
			if other.ReferenceID == p.ReferenceID && other.CallerService == p.CallerService {
				return apperror.AlreadyExists("payment already exists for reference_id: " + p.ReferenceID)
			}
		}
	}
	r.byID[p.ID] = p
	r.Events = append(r.Events, p.PopEvents()...)
	return nil
}

func (r *PaymentRepo) FindByID(_ context.Context, id uuid.UUID) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.byID[id]; ok {
		return p, nil
	}
	return nil, apperror.NotFound("payment not found: " + id.String())
}

func (r *PaymentRepo) FindByReference(_ context.Context, referenceID, callerService string) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.byID {
		if p.ReferenceID == referenceID && p.CallerService == callerService {
			return p, nil
		}
	}
	return nil, apperror.NotFound("payment not found for reference: " + referenceID)
}

func (r *PaymentRepo) FindByGatewayTransactionID(_ context.Context, gatewayTxID string) (*payment.Payment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.byID {
		if p.GatewayTransactionID == gatewayTxID {
			return p, nil
		}
	}
	return nil, apperror.NotFound("payment not found for gateway tx: " + gatewayTxID)
}

func (r *PaymentRepo) FindExpired(context.Context, int) ([]*payment.Payment, error) { return nil, nil }

func (r *PaymentRepo) ListPaginated(context.Context, payment.ListFilter) ([]*payment.Payment, error) {
	return nil, nil
}

func (r *PaymentRepo) UpdateStatus(_ context.Context, id uuid.UUID, status payment.Status, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.byID[id]
	if !ok {
		return apperror.NotFound("payment not found: " + id.String())
	}
	p.Status = status
	return nil
}

// Gateway is a scriptable ports.GatewayPort.
type Gateway struct {
	mu          sync.Mutex
	CreateCalls int
	CreateResp  *ports.GatewayResponse
	CreateErr   error

	Notification *ports.WebhookNotification
	ParseErr     error

	Capabilities map[payment.Method]ports.MethodCapability // nil means every method is fully supported
}

func (g *Gateway) CreateTransaction(context.Context, ports.CreateTransactionRequest) (*ports.GatewayResponse, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.CreateCalls++
	if g.CreateErr != nil {
		return nil, g.CreateErr
	}
	if g.CreateResp != nil {
		return g.CreateResp, nil
	}
	return &ports.GatewayResponse{
		GatewayTransactionID: "gw-" + uuid.NewString(),
		Status:               ports.TransactionPending,
		NextAction:           ports.NextAction{Type: ports.NextActionDisplayVA, VANumber: "1234567890", Bank: "bca"},
		RawResponse:          map[string]any{"status_code": "201"},
	}, nil
}

// Capability reports Capabilities[method], or full support when the map is nil.
func (g *Gateway) Capability(method payment.Method) ports.MethodCapability {
	if g.Capabilities == nil {
		return ports.MethodCapability{Charge: true, Refund: true, PartialRefund: true}
	}
	return g.Capabilities[method]
}

func (g *Gateway) GetTransactionStatus(context.Context, string) (string, error) { return "", nil }
func (g *Gateway) CancelTransaction(context.Context, string) error            { return nil }
func (g *Gateway) CreateRefund(context.Context, string, int64, string) (string, error) {
	return "", nil
}

func (g *Gateway) ParseWebhook([]byte, map[string][]string) (*ports.WebhookNotification, error) {
	if g.ParseErr != nil {
		return nil, g.ParseErr
	}
	return g.Notification, nil
}

// GatewayFactory resolves gateways from a fixed map.
type GatewayFactory map[string]ports.GatewayPort

func (f GatewayFactory) Get(gatewayType string) (ports.GatewayPort, error) {
	if gw, ok := f[gatewayType]; ok {
		return gw, nil
	}
	return nil, apperror.InvalidInput("unsupported gateway: " + gatewayType)
}

// Cache is an in-memory cache satisfying both ports.CachePort and cache.Client.
// TTLs are ignored. Set Err to make every call fail, as if Redis were down.
type Cache struct {
	mu   sync.Mutex
	data map[string]string
	Err  error
}

func NewCache() *Cache { return &Cache{data: map[string]string{}} }

func (c *Cache) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return "", c.Err
	}
	v, ok := c.data[key]
	if !ok {
		return "", apperror.NotFound("cache miss: " + key)
	}
	return v, nil
}

func (c *Cache) Set(_ context.Context, key, value string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return c.Err
	}
	c.data[key] = value
	return nil
}

func (c *Cache) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return false, c.Err
	}
	if _, ok := c.data[key]; ok {
		return false, nil
	}
	c.data[key] = value
	return true, nil
}

func (c *Cache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Err != nil {
		return c.Err
	}
	delete(c.data, key)
	return nil
}

// Has reports whether key is present, for assertions.
func (c *Cache) Has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.data[key]
	return ok
}
