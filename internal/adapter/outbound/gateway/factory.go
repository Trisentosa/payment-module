package gateway

import (
	"fmt"

	"github.com/Trisentosa/payment-module/internal/application/ports"
)

type factory struct {
	adapters map[string]ports.GatewayPort
}

func NewFactory(adapters map[string]ports.GatewayPort) ports.GatewayFactory {
	return &factory{adapters: adapters}
}

func (f *factory) Get(gatewayType string) (ports.GatewayPort, error) {
	a, ok := f.adapters[gatewayType]
	if !ok {
		return nil, fmt.Errorf("unsupported gateway: %s", gatewayType)
	}
	return a, nil
}
