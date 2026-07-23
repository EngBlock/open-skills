package application

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const maxHTTPRedirects = 5

type httpAcquisitionPolicy struct {
	AllowInsecureTransport bool
	Notice                 io.Writer
	warnedInsecureHosts    map[string]bool
}

type httpPolicyError struct {
	message string
}

func (err *httpPolicyError) Error() string { return err.message }

func httpPolicyErrorf(format string, values ...any) error {
	return &httpPolicyError{message: fmt.Sprintf(format, values...)}
}

func isHTTPPolicyError(err error) bool {
	var policyErr *httpPolicyError
	return errors.As(err, &policyErr)
}

type httpAcquisitionResult struct {
	Data      []byte
	Status    int
	FinalURL  *url.URL
	Redirects int
}

func newHTTPAcquisitionPolicy(allowInsecureTransport bool, notice io.Writer) *httpAcquisitionPolicy {
	return &httpAcquisitionPolicy{
		AllowInsecureTransport: allowInsecureTransport,
		Notice:                 notice,
		warnedInsecureHosts:    map[string]bool{},
	}
}

func (policy *httpAcquisitionPolicy) authorize(target *url.URL, role string) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return httpPolicyErrorf("HTTP acquisition requires an HTTP or HTTPS URL with a host")
	}
	if target.User != nil {
		return httpPolicyErrorf("HTTP acquisition URLs must not contain user credentials")
	}
	if target.Scheme != "http" {
		return nil
	}
	if policy == nil || !policy.AllowInsecureTransport {
		return httpPolicyErrorf("plaintext HTTP %s requires --allow-insecure-transport", role)
	}
	key := canonicalHTTPHost(target)
	if !policy.warnedInsecureHosts[key] {
		policy.warnedInsecureHosts[key] = true
		if policy.Notice != nil {
			_, _ = fmt.Fprintf(policy.Notice, "Warning: allowing insecure HTTP %s for %s; source contents may be intercepted or modified.\n", role, redactedHTTPHost(target))
		}
	}
	return nil
}

func fetchHTTPURL(client *http.Client, target *url.URL, headers http.Header, limit int64, policy *httpAcquisitionPolicy) (httpAcquisitionResult, error) {
	if limit < 0 {
		return httpAcquisitionResult{}, errors.New("HTTP response limit must not be negative")
	}
	if err := policy.authorize(target, "source"); err != nil {
		return httpAcquisitionResult{}, err
	}

	current := cloneURL(target)
	requestHeaders := headers.Clone()
	visited := map[string]bool{current.String(): true}
	redirects := 0

	if client == nil {
		client = http.DefaultClient
	}
	boundedClient := *client
	boundedClient.Jar = nil
	boundedClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	for {
		request, err := http.NewRequest(http.MethodGet, current.String(), nil)
		if err != nil {
			return httpAcquisitionResult{}, errors.New("create HTTP acquisition request: invalid URL")
		}
		request.Header = requestHeaders.Clone()
		response, err := boundedClient.Do(request)
		if err != nil {
			return httpAcquisitionResult{}, fmt.Errorf("HTTP request to %s failed", redactedHTTPURL(current))
		}

		if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
			_ = response.Body.Close()
			if redirects >= maxHTTPRedirects {
				return httpAcquisitionResult{}, httpPolicyErrorf("HTTP acquisition exceeded the maximum of %d redirects", maxHTTPRedirects)
			}
			location := response.Header.Get("Location")
			if location == "" {
				return httpAcquisitionResult{}, httpPolicyErrorf("HTTP redirect response is missing a Location header")
			}
			next, err := current.Parse(location)
			if err != nil {
				return httpAcquisitionResult{}, httpPolicyErrorf("HTTP redirect location is invalid")
			}
			if err := policy.authorize(next, "redirect target"); err != nil {
				return httpAcquisitionResult{}, err
			}
			if visited[next.String()] {
				return httpAcquisitionResult{}, httpPolicyErrorf("HTTP redirect loop detected")
			}
			if canonicalHTTPHost(current) != canonicalHTTPHost(next) {
				requestHeaders = headersWithoutCredentials(requestHeaders)
			}
			visited[next.String()] = true
			current = next
			redirects++
			continue
		}

		data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return httpAcquisitionResult{}, fmt.Errorf("read HTTP response from %s failed", redactedHTTPURL(current))
		}
		if int64(len(data)) > limit {
			return httpAcquisitionResult{}, fmt.Errorf("response from %s exceeds the %d-byte limit", redactedHTTPURL(current), limit)
		}
		if redirects > 0 && policy != nil && policy.Notice != nil {
			_, _ = fmt.Fprintf(policy.Notice, "HTTP redirect completed after %d redirects; final host %s.\n", redirects, redactedHTTPHost(current))
		}
		return httpAcquisitionResult{Data: data, Status: response.StatusCode, FinalURL: cloneURL(current), Redirects: redirects}, nil
	}
}

func headersWithoutCredentials(headers http.Header) http.Header {
	result := headers.Clone()
	for name := range result {
		normalized := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
		if strings.Contains(normalized, "authorization") || strings.Contains(normalized, "cookie") || strings.Contains(normalized, "token") || strings.Contains(normalized, "api-key") || strings.Contains(normalized, "apikey") || strings.Contains(normalized, "credential") || strings.Contains(normalized, "secret") {
			delete(result, name)
		}
	}
	return result
}

func canonicalHTTPHost(target *url.URL) string {
	host := strings.ToLower(target.Hostname())
	port := target.Port()
	if port == "" {
		if target.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port)
}

func redactedHTTPHost(target *url.URL) string {
	if target == nil || target.Hostname() == "" {
		return "[redacted host]"
	}
	if host := sanitizeHuman(strings.ToLower(target.Host)); host != "" {
		return host
	}
	return "[redacted host]"
}

func redactedHTTPURL(target *url.URL) string {
	if target == nil {
		return "[redacted source]"
	}
	copy := cloneURL(target)
	copy.User = nil
	copy.RawQuery = ""
	copy.ForceQuery = false
	copy.Fragment = ""
	return credentialFreeSource(copy.String())
}
