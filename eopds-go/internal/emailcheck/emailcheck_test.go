package emailcheck

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// fakeResolver — подменный DNS-резолвер.
type fakeResolver struct {
	mx   map[string][]*net.MX
	host map[string][]string
	err  map[string]error
}

func (f *fakeResolver) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if err := f.err[name]; err != nil {
		return nil, err
	}
	return f.mx[name], nil
}

func (f *fakeResolver) LookupHost(_ context.Context, name string) ([]string, error) {
	if err := f.err[name]; err != nil {
		return nil, err
	}
	return f.host[name], nil
}

func TestValidSyntax(t *testing.T) {
	ok := []string{"a@b.co", "user.name+tag@mail.example.com"}
	bad := []string{"", "a", "a@", "a@b", "@b.co", "a b@c.co", "user@localhost"}
	for _, a := range ok {
		if !ValidSyntax(a) {
			t.Errorf("%q must be valid", a)
		}
	}
	for _, a := range bad {
		if ValidSyntax(a) {
			t.Errorf("%q must be invalid", a)
		}
	}
}

func TestCheck(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", Name: "nodomain.test", IsNotFound: true}
	res := &fakeResolver{
		mx:   map[string][]*net.MX{"mail.example.com": {{Host: "mx.example.com.", Pref: 10}}},
		host: map[string][]string{"hostonly.example.com": {"192.0.2.1"}},
		err: map[string]error{
			"nodomain.test": notFound,
			"tempfail.test": &net.DNSError{Err: "timeout", Name: "tempfail.test", IsTimeout: true},
		},
	}
	c := NewCheckerWith(res)

	tests := []struct {
		addr   string
		status string
	}{
		{"user@mail.example.com", StatusOK},
		{"USER@MAIL.EXAMPLE.COM", StatusOK},
		{"user@hostonly.example.com", StatusOK},
		{"bad@@example.com", StatusSyntax},
		{"user@nodomain.test", StatusNoDomain},
		{"user@tempfail.test", StatusDNSError},
	}
	ctx := context.Background()
	for _, tc := range tests {
		got := c.Check(ctx, tc.addr)
		if got.Status != tc.status {
			t.Errorf("%q: status %q, want %q (%s)", tc.addr, got.Status, tc.status, got.Err)
		}
		if got.CheckedAt == 0 {
			t.Errorf("%q: CheckedAt must be set", tc.addr)
		}
	}

	// MX предпочтительнее A-записи.
	r := c.Check(ctx, "user@mail.example.com")
	if len(r.Records) != 1 || r.Records[0] != "mx" {
		t.Errorf("expected mx record, got %v", r.Records)
	}
	r = c.Check(ctx, "user@hostonly.example.com")
	if len(r.Records) != 1 || r.Records[0] != "a" {
		t.Errorf("expected a record, got %v", r.Records)
	}

	if !c.Check(ctx, "user@mail.example.com").Valid() {
		t.Error("ok address must be valid")
	}
	if c.Check(ctx, "user@nodomain.test").Valid() {
		t.Error("no domain address must be invalid")
	}
}

func TestCheckCache(t *testing.T) {
	calls := 0
	res := &countingResolver{calls: &calls,
		inner: &fakeResolver{host: map[string][]string{"c.example.com": {"192.0.2.1"}}}}
	c := NewCheckerWith(res)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if r := c.Check(ctx, "user@c.example.com"); !r.Valid() {
			t.Fatalf("expected valid, got %s", r.Status)
		}
	}
	if calls != 1 {
		t.Errorf("expected 1 resolver lookup (cache), got %d", calls)
	}
}

// countingResolver считает вызовы LookupHost.
type countingResolver struct {
	inner *fakeResolver
	calls *int
}

func (r *countingResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return r.inner.LookupMX(ctx, name)
}

func (r *countingResolver) LookupHost(ctx context.Context, name string) ([]string, error) {
	*r.calls++
	return r.inner.LookupHost(ctx, name)
}

func TestReasonKey(t *testing.T) {
	for _, st := range []string{StatusOK, StatusSyntax, StatusNoDomain, StatusDNSError, StatusNotNeeded, StatusUnknown, "other"} {
		key := Result{Status: st}.ReasonKey()
		if !strings.HasPrefix(key, "mail.check.") {
			t.Errorf("status %q: bad key %q", st, key)
		}
	}
	var dnsErr *net.DNSError
	if !errors.As(&net.DNSError{IsNotFound: true}, &dnsErr) || !isNotFound(dnsErr) {
		t.Error("isNotFound must detect not-found errors")
	}
	if isNotFound(&net.DNSError{IsTimeout: true}) {
		t.Error("timeout is not a not-found error")
	}
}
