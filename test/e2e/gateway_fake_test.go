//go:build e2e

package e2e_test

import (
	"fmt"
	"time"
)

const (
	serverKey = "SB-Mid-server-e2e-test-key"

	// PayGate gives up on Midtrans after gatewayTimeout; the fake answers LOST charges
	// after lostReplyDelay, so PayGate never sees those replies.
	gatewayTimeout = 1500 * time.Millisecond
	lostReplyDelay = 2500 * time.Millisecond
)

var midtrans *fakeMidtrans

func serverPort() (int, error) { return freePort() }

// startGateway starts the fake Midtrans and points PayGate at it.
func startGateway(_, paygateURL string) (gatewaySetup, error) {
	midtrans = newFakeMidtrans(serverKey, lostReplyDelay)
	midtrans.webhookURL = paygateURL + "/webhooks/midtrans"
	return gatewaySetup{
		env: []string{
			"MIDTRANS_SERVER_KEY=" + serverKey,
			"MIDTRANS_BASE_URL=" + midtrans.URL(),
			"MIDTRANS_TIMEOUT_MS=" + fmt.Sprint(gatewayTimeout.Milliseconds()),
			"RECONCILE_CHARGE_GRACE=3s",
		},
		stop: midtrans.Close,
	}, nil
}
