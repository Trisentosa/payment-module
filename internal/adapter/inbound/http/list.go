package http

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/query"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// listParams are the paging and time-window parameters the list endpoints share.
type listParams struct {
	From, To *time.Time
	Cursor   string
	Limit    int
}

// parseListParams rejects malformed values instead of ignoring them: a dropped cursor
// would quietly restart the listing at page one.
func parseListParams(q url.Values) (listParams, error) {
	p := listParams{Cursor: q.Get("cursor")}
	for name, dst := range map[string]**time.Time{"from": &p.From, "to": &p.To} {
		v := q.Get(name)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return p, apperror.InvalidInput(name + " must be an RFC 3339 timestamp")
		}
		*dst = &t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return p, apperror.InvalidInput("limit must be a positive integer")
		}
		p.Limit = n
	}
	return p, nil
}

type paymentPageMeta struct {
	Count      int    `json:"count"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

type paymentPage struct {
	Data []*query.PaymentDTO `json:"data"`
	Meta paymentPageMeta     `json:"meta"`
}

func writePaymentList(w http.ResponseWriter, result *query.ListPaymentsResult) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(paymentPage{
		Data: result.Payments,
		Meta: paymentPageMeta{
			Count:      len(result.Payments),
			NextCursor: result.NextCursor,
			HasMore:    result.NextCursor != "",
		},
	})
}
