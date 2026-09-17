package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

// WebhookTimeout bounds one delivery, so a slow receiver can't hold up the
// next rule for long.
const WebhookTimeout = 5 * time.Second

// Webhook POSTs alerts as JSON.
type Webhook struct {
	client *http.Client
}

// NewWebhook sends with client, or when it is nil, with a client that
// doesn't follow redirects: a rule's URL is where its alerts go, and a
// redirected POST would silently become a GET or go somewhere else.
func NewWebhook(client *http.Client) *Webhook {
	if client == nil {
		client = &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &Webhook{client: client}
}

// Send posts alert to target, and fails unless the receiver answers 2xx
// within WebhookTimeout. The error never contains the URL, which may hold a
// secret.
func (w *Webhook) Send(ctx context.Context, target string, alert gen.Alert) error {
	body, err := json.Marshal(alert)
	if err != nil {
		return errors.Internalf(err, "cannot encode alert")
	}
	ctx, cancel := context.WithTimeout(ctx, WebhookTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cannot build webhook request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "loghub")

	res, err := w.client.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return err
	}
	defer res.Body.Close()
	// Drained, so the connection can be reused, but not without limit.
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("webhook answered %s", res.Status)
	}
	return nil
}
