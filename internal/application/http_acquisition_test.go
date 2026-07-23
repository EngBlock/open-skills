package application

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPAcquisitionFollowsOnlyBoundedRedirectsAndReportsFinalHost(t *testing.T) {
	var requests atomic.Int32
	var finalAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		hop := int(requests.Load()) - 1
		if hop < maxHTTPRedirects {
			http.Redirect(response, request, fmt.Sprintf("/hop/%d", hop+1), http.StatusFound)
			return
		}
		finalAuthorization = request.Header.Get("Authorization")
		_, _ = response.Write([]byte("ok"))
	}))
	defer server.Close()

	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var notice bytes.Buffer
	result, err := fetchHTTPURL(server.Client(), target, http.Header{"Authorization": []string{"Bearer same-host-secret"}}, 16, newHTTPAcquisitionPolicy(true, &notice))
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != "ok" || result.Redirects != maxHTTPRedirects || result.FinalURL.Host != target.Host {
		t.Fatalf("redirect result = %#v", result)
	}
	if finalAuthorization != "Bearer same-host-secret" {
		t.Fatalf("same-host authorization = %q", finalAuthorization)
	}
	if !strings.Contains(notice.String(), fmt.Sprintf("final host %s", target.Host)) || !strings.Contains(notice.String(), fmt.Sprintf("%d redirects", maxHTTPRedirects)) {
		t.Fatalf("redirect notice = %q", notice.String())
	}

	requests.Store(0)
	overLimit := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		hop := requests.Add(1)
		http.Redirect(response, request, fmt.Sprintf("/hop/%d", hop), http.StatusFound)
	}))
	defer overLimit.Close()
	target, err = url.Parse(overLimit.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetchHTTPURL(overLimit.Client(), target, nil, 16, newHTTPAcquisitionPolicy(true, &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("maximum of %d redirects", maxHTTPRedirects)) {
		t.Fatalf("excessive redirect error = %v", err)
	}
}

func TestHTTPAcquisitionRejectsRedirectLoops(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		target := "/one"
		if request.URL.Path == "/one" {
			target = "/two"
		}
		http.Redirect(response, request, target, http.StatusFound)
	}))
	defer server.Close()
	target, err := url.Parse(server.URL + "/one")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fetchHTTPURL(server.Client(), target, nil, 16, newHTTPAcquisitionPolicy(true, &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "redirect loop") {
		t.Fatalf("loop error = %v", err)
	}
}

func TestHTTPAcquisitionDropsCredentialsAcrossHosts(t *testing.T) {
	var destinationAuthorization, destinationProxyAuthorization, destinationCookie, destinationAPIKey, destinationCustomAuthorization, destinationCustomCookie string
	destination := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		destinationAuthorization = request.Header.Get("Authorization")
		destinationProxyAuthorization = request.Header.Get("Proxy-Authorization")
		destinationCookie = request.Header.Get("Cookie")
		destinationAPIKey = request.Header.Get("X-API-Key")
		destinationCustomAuthorization = request.Header.Get("X-Authorization")
		destinationCustomCookie = request.Header.Get("X-Cookie")
		_, _ = response.Write([]byte("ok"))
	}))
	defer destination.Close()

	var sourceAuthorization string
	source := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		sourceAuthorization = request.Header.Get("Authorization")
		http.Redirect(response, request, destination.URL+"/skill", http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	target, err := url.Parse(source.URL)
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{
		"Authorization":       []string{"Bearer acquisition-secret"},
		"Proxy-Authorization": []string{"Basic proxy-secret"},
		"Cookie":              []string{"session=cookie-secret"},
		"X-API-Key":           []string{"api-key-secret"},
		"X-Authorization":     []string{"custom-authorization-secret"},
		"X-Cookie":            []string{"custom-cookie-secret"},
	}
	result, err := fetchHTTPURL(source.Client(), target, headers, 16, newHTTPAcquisitionPolicy(true, &bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	if sourceAuthorization != "Bearer acquisition-secret" {
		t.Fatalf("source authorization = %q", sourceAuthorization)
	}
	if destinationAuthorization != "" || destinationProxyAuthorization != "" || destinationCookie != "" || destinationAPIKey != "" || destinationCustomAuthorization != "" || destinationCustomCookie != "" {
		t.Fatalf("credentials reached destination: authorization=%q proxy=%q cookie=%q api-key=%q custom-authorization=%q custom-cookie=%q", destinationAuthorization, destinationProxyAuthorization, destinationCookie, destinationAPIKey, destinationCustomAuthorization, destinationCustomCookie)
	}
	if result.FinalURL.Host != strings.TrimPrefix(destination.URL, "http://") {
		t.Fatalf("final host = %q", result.FinalURL.Host)
	}
}

func TestHTTPAcquisitionDowngradeRequiresDedicatedAuthorizationAndWarning(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		destinationRequests.Add(1)
		_, _ = response.Write([]byte("ok"))
	}))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, destination.URL+"/skill", http.StatusFound)
	}))
	defer source.Close()
	target, err := url.Parse(source.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = fetchHTTPURL(source.Client(), target, nil, 16, newHTTPAcquisitionPolicy(false, &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "--allow-insecure-transport") || destinationRequests.Load() != 0 {
		t.Fatalf("unauthorized downgrade = error %v destination requests %d", err, destinationRequests.Load())
	}

	var notice bytes.Buffer
	result, err := fetchHTTPURL(source.Client(), target, nil, 16, newHTTPAcquisitionPolicy(true, &notice))
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != "ok" || destinationRequests.Load() != 1 || !strings.Contains(notice.String(), "Warning: allowing insecure HTTP redirect target") {
		t.Fatalf("authorized downgrade = result %#v requests %d notice %q", result, destinationRequests.Load(), notice.String())
	}
}

func TestHTTPAcquisitionRedactsCredentialedRedirectDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Location", "http://token-user:password-secret@example.invalid/skill?access_token=query-secret")
		response.WriteHeader(http.StatusFound)
		_, _ = response.Write([]byte("response-body-secret"))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var notice bytes.Buffer
	_, err = fetchHTTPURL(server.Client(), target, nil, 16, newHTTPAcquisitionPolicy(true, &notice))
	if err == nil {
		t.Fatal("credentialed redirect unexpectedly succeeded")
	}
	combined := err.Error() + notice.String()
	for _, secret := range []string{"token-user", "password-secret", "query-secret", "access_token", "response-body-secret"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("diagnostics leaked %q: %q", secret, combined)
		}
	}
}
