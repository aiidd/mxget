package sreq

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	neturl "net/url"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

const (
	// DefaultTimeout is the preset timeout.
	DefaultTimeout = 120 * time.Second
)

var (
	// GlobalClient is a sreq Client used by the global functions such as Get, Post, etc.
	GlobalClient = New()
)

type (
	// Client wraps the raw HTTP client.
	// Do not modify the client across Goroutines!
	// You should reuse it as possible after initialized.
	Client struct {
		RawClient *http.Client

		beforeRequestHooks []BeforeRequestHook
		afterResponseHooks []AfterResponseHook
	}
)

// New returns a new Client.
// It's a clone of GlobalClient indeed.
func New() *Client {
	jar, _ := cookiejar.New(&cookiejar.Options{
		PublicSuffixList: publicsuffix.List,
	})
	rawClient := &http.Client{
		Transport: DefaultTransport(),
		Jar:       jar,
		Timeout:   DefaultTimeout,
	}
	client := &Client{
		RawClient: rawClient,
	}
	return client
}

func (c *Client) httpTransport() (*http.Transport, error) {
	t, ok := c.RawClient.Transport.(*http.Transport)
	if !ok {
		return nil, ErrUnexpectedTransport
	}

	return t, nil
}

// SetTransport sets transport of the HTTP client.
func (c *Client) SetTransport(transport http.RoundTripper) *Client {
	c.RawClient.Transport = transport
	return c
}

// SetRedirect sets policy of the HTTP client for handling redirects.
func (c *Client) SetRedirect(policy func(req *http.Request, via []*http.Request) error) *Client {
	c.RawClient.CheckRedirect = policy
	return c
}

// DisableRedirect is a retry policy to disable redirects.
func DisableRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// SetCookieJar sets cookie jar of the HTTP client.
func (c *Client) SetCookieJar(jar http.CookieJar) *Client {
	c.RawClient.Jar = jar
	return c
}

// DisableSession makes the HTTP client not use cookie jar.
// Only use if you don't want to keep session for the next HTTP request.
func (c *Client) DisableSession() *Client {
	c.SetCookieJar(nil)
	return c
}

// SetTimeout sets timeout of the HTTP client.
func (c *Client) SetTimeout(timeout time.Duration) *Client {
	c.RawClient.Timeout = timeout
	return c
}

// SetProxy sets proxy of the HTTP client.
func (c *Client) SetProxy(proxy func(*http.Request) (*neturl.URL, error)) (*Client, error) {
	t, err := c.httpTransport()
	if err != nil {
		return c, &Error{
			Op:  "Client.SetProxy",
			Err: err,
		}
	}

	t.Proxy = proxy
	c.RawClient.Transport = t
	return c, nil
}

// SetProxyFromURL sets proxy of the HTTP client from a URL.
func (c *Client) SetProxyFromURL(url string) (*Client, error) {
	fixedURL, err := neturl.Parse(url)
	if err != nil {
		return c, &Error{
			Op:  "Client.SetProxyFromURL",
			Err: err,
		}
	}

	return c.SetProxy(http.ProxyURL(fixedURL))
}

// DisableProxy makes the HTTP client not use proxy.
func (c *Client) DisableProxy() (*Client, error) {
	return c.SetProxy(nil)
}

// SetTLSClientConfig sets TLS configuration of the HTTP client.
func (c *Client) SetTLSClientConfig(config *tls.Config) (*Client, error) {
	t, err := c.httpTransport()
	if err != nil {
		return c, &Error{
			Op:  "Client.SetTLSClientConfig",
			Err: err,
		}
	}

	t.TLSClientConfig = config
	c.RawClient.Transport = t
	return c, nil
}

// AppendClientCerts appends client certificates to the HTTP client.
func (c *Client) AppendClientCerts(certs ...tls.Certificate) (*Client, error) {
	t, err := c.httpTransport()
	if err != nil {
		return c, &Error{
			Op:  "Client.AppendClientCerts",
			Err: err,
		}
	}

	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}

	t.TLSClientConfig.Certificates = append(t.TLSClientConfig.Certificates, certs...)
	c.RawClient.Transport = t
	return c, nil
}

// AppendRootCerts appends root certificates from a pem file to the HTTP client.
func (c *Client) AppendRootCerts(pemFile string) (*Client, error) {
	pemCerts, err := ioutil.ReadFile(pemFile)
	if err != nil {
		return c, &Error{
			Op:  "Client.AppendRootCerts",
			Err: err,
		}
	}

	t, err := c.httpTransport()
	if err != nil {
		return c, &Error{
			Op:  "Client.AppendRootCerts",
			Err: err,
		}
	}

	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}
	if t.TLSClientConfig.RootCAs == nil {
		t.TLSClientConfig.RootCAs = x509.NewCertPool()
	}
	t.TLSClientConfig.RootCAs.AppendCertsFromPEM(pemCerts)
	c.RawClient.Transport = t
	return c, nil
}

// DisableVerify makes the HTTP client not verify the server's TLS certificate.
func (c *Client) DisableVerify() (*Client, error) {
	t, err := c.httpTransport()
	if err != nil {
		return c, &Error{
			Op:  "Client.DisableVerify",
			Err: err,
		}
	}

	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{}
	}

	t.TLSClientConfig.InsecureSkipVerify = true
	c.RawClient.Transport = t
	return c, nil
}

// SetCookies sets cookies to cookie jar for the given URL.
func (c *Client) SetCookies(url string, cookies ...*http.Cookie) (*Client, error) {
	if c.RawClient.Jar == nil {
		return c, &Error{
			Op:  "Client.SetCookies",
			Err: ErrNilCookieJar,
		}
	}

	u, err := neturl.Parse(url)
	if err != nil {
		return c, &Error{
			Op:  "Client.SetCookies",
			Err: err,
		}
	}

	c.RawClient.Jar.SetCookies(u, cookies)
	return c, nil
}

// OnBeforeRequest appends request hooks into the before request chain.
func (c *Client) OnBeforeRequest(hooks ...BeforeRequestHook) *Client {
	c.beforeRequestHooks = append(c.beforeRequestHooks, hooks...)
	return c
}

// OnAfterResponse appends response hooks into the after response chain.
func (c *Client) OnAfterResponse(hooks ...AfterResponseHook) *Client {
	c.afterResponseHooks = append(c.afterResponseHooks, hooks...)
	return c
}

// Get makes a GET HTTP request.
func Get(url string, opts ...RequestOption) *Response {
	return GlobalClient.Get(url, opts...)
}

// Get makes a GET HTTP request.
func (c *Client) Get(url string, opts ...RequestOption) *Response {
	return c.Send(MethodGet, url, opts...)
}

// Head makes a HEAD HTTP request.
func Head(url string, opts ...RequestOption) *Response {
	return GlobalClient.Head(url, opts...)
}

// Head makes a HEAD HTTP request.
func (c *Client) Head(url string, opts ...RequestOption) *Response {
	return c.Send(MethodHead, url, opts...)
}

// Post makes a POST HTTP request.
func Post(url string, opts ...RequestOption) *Response {
	return GlobalClient.Post(url, opts...)
}

// Post makes a POST HTTP request.
func (c *Client) Post(url string, opts ...RequestOption) *Response {
	return c.Send(MethodPost, url, opts...)
}

// Put makes a PUT HTTP request.
func Put(url string, opts ...RequestOption) *Response {
	return GlobalClient.Put(url, opts...)
}

// Put makes a PUT HTTP request.
func (c *Client) Put(url string, opts ...RequestOption) *Response {
	return GlobalClient.Send(MethodPut, url, opts...)
}

// Patch makes a PATCH HTTP request.
func Patch(url string, opts ...RequestOption) *Response {
	return GlobalClient.Patch(url, opts...)
}

// Patch makes a PATCH HTTP request.
func (c *Client) Patch(url string, opts ...RequestOption) *Response {
	return c.Send(MethodPatch, url, opts...)
}

// Delete makes a DELETE HTTP request.
func Delete(url string, opts ...RequestOption) *Response {
	return GlobalClient.Delete(url, opts...)
}

// Delete makes a DELETE HTTP request.
func (c *Client) Delete(url string, opts ...RequestOption) *Response {
	return c.Send(MethodDelete, url, opts...)
}

// Send makes an HTTP request using a specified method.
func Send(method string, url string, opts ...RequestOption) *Response {
	return GlobalClient.Send(method, url, opts...)
}

// Send makes an HTTP request using a specified method.
func (c *Client) Send(method string, url string, opts ...RequestOption) *Response {
	req, err := NewRequest(method, url, opts...)
	if err != nil {
		return &Response{err: err}
	}

	return c.Do(req)
}

// FilterCookies returns the cookies to send in a request for the given URL from cookie jar.
func (c *Client) FilterCookies(url string) ([]*http.Cookie, error) {
	if c.RawClient.Jar == nil {
		return nil, ErrNilCookieJar
	}

	u, err := neturl.Parse(url)
	if err != nil {
		return nil, err
	}

	return c.RawClient.Jar.Cookies(u), nil
}

// FilterCookie returns the named cookie to send in a request for the given URL from cookie jar.
func (c *Client) FilterCookie(url string, name string) (*http.Cookie, error) {
	cookies, err := c.FilterCookies(url)
	if err != nil {
		return nil, err
	}

	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie, nil
		}
	}

	return nil, ErrNoCookie
}

// Do sends a request and returns its  response.
func (c *Client) Do(req *Request) *Response {
	resp := new(Response)

	if err := c.onBeforeRequest(req); err != nil {
		resp.err = err
		return resp
	}

	req.Sync()
	c.doWithRetry(req, resp)
	c.onAfterResponse(resp)
	return resp
}

func (c *Client) onBeforeRequest(req *Request) error {
	var err error
	for _, hook := range c.beforeRequestHooks {
		if err = hook(req); err != nil {
			break
		}
	}
	return err
}

func drainBody(body io.ReadCloser) (r io.Reader, err error) {
	var buf bytes.Buffer
	if _, err = buf.ReadFrom(body); err != nil {
		return nil, err
	}
	if err = body.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

func (c *Client) doWithRetry(req *Request, resp *Response) {
	retry := req.Retry.Merge(defaultRetry)
	if retry.MaxAttempts > 1 && req.RawRequest.Body != nil &&
		req.RawRequest.GetBody == nil {
		body, _ := drainBody(req.RawRequest.Body)
		req.SetBody(body)
	}

	ctx := req.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	req.RawRequest = req.RawRequest.WithContext(ctx)

	var err error
	for i := 0; i < retry.MaxAttempts; i++ {
		resp.RawResponse, resp.err = c.do(req)
		if err = ctx.Err(); err != nil {
			resp.err = err
			return
		}

		if i == retry.MaxAttempts-1 {
			return
		}

		shouldRetry := retry.Trigger(resp)
		if !shouldRetry {
			return
		}

		if req.RawRequest.GetBody != nil {
			req.RawRequest.Body, _ = req.RawRequest.GetBody()
		}

		select {
		case <-time.After(retry.Backoff(retry.WaitTime, retry.MaxWaitTime, i, resp)):
		case <-ctx.Done():
			resp.err = ctx.Err()
			return
		}
	}
}

func (c *Client) do(req *Request) (*http.Response, error) {
	rawResponse, err := c.RawClient.Do(req.RawRequest)
	if err != nil {
		return rawResponse, err
	}

	select {
	case err = <-req.err:
		return rawResponse, err
	default:
	}

	if strings.EqualFold(rawResponse.Header.Get("Content-Encoding"), "gzip") &&
		rawResponse.ContentLength != 0 {
		if _, ok := rawResponse.Body.(*gzip.Reader); !ok {
			body, err := gzip.NewReader(rawResponse.Body)
			rawResponse.Body.Close()
			rawResponse.Body = body
			return rawResponse, err
		}
	}

	return rawResponse, nil
}

func (c *Client) onAfterResponse(resp *Response) {
	for _, hook := range c.afterResponseHooks {
		hook(resp)
		if resp.err != nil {
			break
		}
	}
}
