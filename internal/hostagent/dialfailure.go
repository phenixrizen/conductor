package hostagent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
)

// dialFailure is a failed control dial's error in this package's own words:
// the host it was dialing and, as far as the error's kind tells, why. The
// dial's own error is never passed on as it is. net/http's names the URL it
// was dialing, a redirect's whole, and quotes a Location it could not read
// as it came; the WebSocket library's and the certificate checks' quote
// what the server sent (a handshake's headers, a certificate's names).
// host is the control URL's; a redirect's host replaces it. A refusal the
// server answered with (RefusedError) and a redirect the dial's policy
// refused (redirectRefused) are the caller's to say first.
func dialFailure(err error, resp *http.Response, host string) error {
	if errors.Is(err, errNotPaced) {
		return errNotPaced
	}
	var u *url.Error
	if errors.As(err, &u) {
		if h := serverHost(u.URL); h != "" {
			host = h
		}
		err = u.Err
	} else if resp != nil {
		// The server answered, and the WebSocket library refused the answer.
		if resp.Request != nil && resp.Request.URL != nil {
			host = resp.Request.URL.Host
		}
		if resp.StatusCode == http.StatusSwitchingProtocols {
			return fmt.Errorf("host: the control connection to %s failed: its WebSocket handshake is not one this host accepts", host)
		}
		return fmt.Errorf("host: the control connection to %s failed: it answered HTTP %d, not a WebSocket upgrade", host, resp.StatusCode)
	}
	if why := failureCause(err); why != "" {
		return fmt.Errorf("host: the control connection to %s failed: %s", host, why)
	}
	return fmt.Errorf("host: the control connection to %s failed", host)
}

// offMachine is the owner's server's dial refusing an address that is not
// this machine's (ownersServerClient): the address it resolved, ip:port.
type offMachine struct{ address string }

func (e *offMachine) Error() string { return "host: " + e.address + " is not this machine" }

// failureCause is why a dial failed, in fixed words by the error's kind;
// "" for a kind it does not know, whose words it does not repeat either.
func failureCause(err error) string {
	var (
		off       *offMachine
		dns       *net.DNSError
		authority x509.UnknownAuthorityError
		name      x509.HostnameError
		invalid   x509.CertificateInvalidError
		record    tls.RecordHeaderError
		op        *net.OpError
		timeout   interface{ Timeout() bool }
	)
	switch {
	case errors.Is(err, context.Canceled):
		return "stopped"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "timed out"
	case errors.As(err, &off):
		return "its address " + off.address + " is not this machine"
	case errors.As(err, &dns):
		if dns.IsTimeout {
			return "looking up its name timed out"
		}
		return "its name does not resolve"
	case errors.As(err, &op) && op.Op == "proxyconnect":
		return "the proxy the environment names (HTTPS_PROXY, HTTP_PROXY) did not connect it"
	case errors.As(err, &authority):
		return "its certificate is signed by an authority this machine does not trust"
	case errors.As(err, &name):
		return "its certificate is for another name"
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return "its certificate has expired, or is not valid yet"
		}
		return "its certificate is not valid"
	case errors.As(err, &record), errors.Is(err, http.ErrSchemeMismatch):
		return "it did not answer in TLS"
	case errors.As(err, &op) && op.Op == "remote error":
		return "it ended the TLS handshake"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "the connection was refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "the connection was reset"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "it cannot be reached from this machine"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "the connection closed before it answered"
	case errors.As(err, &timeout) && timeout.Timeout():
		return "timed out"
	}
	return ""
}
