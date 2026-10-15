//go:build sandbox

package e2e_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// The Midtrans sandbox simulators are plain HTML forms (no CSRF token, no captcha), so the
// drill can pay through them the way a person's browser would. There is no simulator API;
// if Midtrans changes these pages, run the drill with SANDBOX_MANUAL=1 and pay by hand.
const simulatorURL = "https://simulator.sandbox.midtrans.com"

type simulator struct{ c *http.Client }

func newSimulator() *simulator {
	jar, _ := cookiejar.New(nil) // the simulators keep the inquiry in a session
	return &simulator{c: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

// payVA pays a virtual account the way a bank customer would.
func (s *simulator) payVA(bank, vaNumber string) error {
	var page string
	var at *url.URL
	var err error
	switch bank {
	case "bca", "bni":
		page, at, err = s.send(http.MethodPost, simulatorURL+"/"+bank+"/va/inquiry", url.Values{"va_number": {vaNumber}})
	default: // permata, bri, cimb, ... share one simulator
		page, at, err = s.send(http.MethodPost, simulatorURL+"/openapi/va/inquiry", url.Values{"vaNumber": {vaNumber}, "bank": {bank}})
	}
	if err != nil {
		return fmt.Errorf("%s VA inquiry: %w", bank, err)
	}
	var set url.Values
	if bank != "bca" && bank != "bni" {
		set = url.Values{"bank": {bank}} // the page's script adds it; without it the simulator answers 500
	}
	return s.submitAndCheck(page, at, set)
}

// payShopeePay opens the ShopeePay deeplink and confirms with the sandbox PIN.
func (s *simulator) payShopeePay(deeplink string) error {
	page, at, err := s.send(http.MethodGet, deeplink, nil)
	if err != nil {
		return fmt.Errorf("open ShopeePay deeplink: %w", err)
	}
	return s.submitAndCheck(page, at, url.Values{"pin": {"123456"}})
}

// submitAndCheck submits the page's form, with set overriding its fields, and reads the
// simulator's verdict from the result page.
func (s *simulator) submitAndCheck(page string, at *url.URL, set url.Values) error {
	f, err := firstForm(page)
	if err != nil {
		return fmt.Errorf("%s: %w; page says: %s", at, err, pageText(page))
	}
	for k, v := range set {
		f.fields[k] = v
	}
	target, err := at.Parse(f.action)
	if err != nil {
		return err
	}
	result, _, err := s.send(f.method, target.String(), f.fields)
	if err != nil {
		return err
	}
	return verdict(result)
}

func (s *simulator) send(method, target string, form url.Values) (string, *url.URL, error) {
	var body io.Reader
	if method == http.MethodGet && form != nil {
		target += "?" + form.Encode()
	} else if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return "", nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := s.c.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return "", nil, err
	}
	if res.StatusCode >= 400 {
		return "", nil, fmt.Errorf("%s %s: HTTP %d: %s", method, target, res.StatusCode, pageText(string(b)))
	}
	return string(b), res.Request.URL, nil
}

type form struct {
	action, method string
	fields         url.Values
}

// firstForm returns the first form on the page that has fields: hidden and text inputs,
// and the first option of each select.
func firstForm(page string) (form, error) {
	z := html.NewTokenizer(strings.NewReader(page))
	var cur *form
	var selectName string
	for {
		switch z.Next() {
		case html.ErrorToken:
			return form{}, fmt.Errorf("no form with fields")
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			a := map[string]string{}
			for _, at := range t.Attr {
				a[at.Key] = at.Val
			}
			switch t.Data {
			case "form":
				method := strings.ToUpper(a["method"])
				if method == "" {
					method = http.MethodGet
				}
				cur = &form{action: a["action"], method: method, fields: url.Values{}}
			case "input":
				if cur != nil && a["name"] != "" && a["type"] != "submit" {
					cur.fields.Set(a["name"], a["value"])
				}
			case "select":
				selectName = a["name"]
			case "option":
				if cur != nil && selectName != "" && cur.fields.Get(selectName) == "" {
					cur.fields.Set(selectName, a["value"])
				}
			}
		case html.EndTagToken:
			switch z.Token().Data {
			case "select":
				selectName = ""
			case "form":
				if cur != nil && len(cur.fields) > 0 {
					return *cur, nil
				}
				cur = nil
			}
		}
	}
}

var (
	failedText  = regexp.MustCompile(`(?i)unsuccessful|failed|invalid|not found|expired`)
	successText = regexp.MustCompile(`(?i)success`)
)

func verdict(page string) error {
	text := pageText(page)
	switch {
	case failedText.MatchString(text):
		return fmt.Errorf("simulator refused the payment: %s", text)
	case successText.MatchString(text):
		return nil
	default:
		return fmt.Errorf("simulator gave no verdict: %s", text)
	}
}

var (
	scripts = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>`)
	tags    = regexp.MustCompile(`<[^>]+>`)
	spaces  = regexp.MustCompile(`\s+`)
)

// pageText is the visible text of the page's main content, shortened.
func pageText(page string) string {
	if i := strings.Index(page, "content-body"); i >= 0 {
		page = page[i:]
	}
	t := strings.TrimSpace(spaces.ReplaceAllString(tags.ReplaceAllString(scripts.ReplaceAllString(page, " "), " "), " "))
	t = strings.TrimPrefix(t, `content-body">`)
	if len(t) > 200 {
		t = t[:200]
	}
	return t
}
