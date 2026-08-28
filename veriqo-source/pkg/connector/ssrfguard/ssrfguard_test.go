package ssrfguard

import (
	"net"
	"testing"
)

func fakeResolver(m map[string][]net.IP) Resolver {
	return func(host string) ([]net.IP, error) {
		if ips, ok := m[host]; ok {
			return ips, nil
		}
		return nil, nil
	}
}

func TestIsBlockedAddressCoversEveryDangerousRange(t *testing.T) {
	cases := []struct {
		name string
		ip   string
	}{
		{"loopback v4", "127.0.0.1"},
		{"loopback v6", "::1"},
		{"link-local v4 (covers cloud metadata 169.254.169.254)", "169.254.169.254"},
		{"link-local v6", "fe80::1"},
		{"private RFC1918 10/8", "10.0.0.1"},
		{"private RFC1918 172.16/12", "172.16.0.1"},
		{"private RFC1918 192.168/16", "192.168.1.1"},
		{"private RFC4193 v6", "fd00::1"},
		{"unspecified v4", "0.0.0.0"},
		{"unspecified v6", "::"},
		{"multicast", "224.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ip := net.ParseIP(c.ip)
			if ip == nil {
				t.Fatalf("test bug: %q did not parse as an IP", c.ip)
			}
			if !IsBlockedAddress(ip) {
				t.Fatalf("IsBlockedAddress(%s) = false, want true", c.ip)
			}
		})
	}
}

func TestIsBlockedAddressAllowsRealPublicAddresses(t *testing.T) {
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "203.0.113.5"} {
		ip := net.ParseIP(s)
		if IsBlockedAddress(ip) {
			t.Fatalf("IsBlockedAddress(%s) = true, want false (a real public address)", s)
		}
	}
}

func TestIsBlockedAddressRefusesNil(t *testing.T) {
	if !IsBlockedAddress(nil) {
		t.Fatal("IsBlockedAddress(nil) = false, want true (fail closed on no address at all)")
	}
}

func TestValidateURLRefusesEmptyAndMalformed(t *testing.T) {
	if err := ValidateURL("", []string{"https"}, nil); err != ErrEmptyURL {
		t.Fatalf("expected ErrEmptyURL, got %v", err)
	}
	if err := ValidateURL("://not a url", []string{"https"}, nil); err == nil {
		t.Fatal("expected an error for a malformed URL")
	}
}

func TestValidateURLRefusesDisallowedScheme(t *testing.T) {
	err := ValidateURL("file:///etc/passwd", []string{"https", "wss"}, fakeResolver(nil))
	if err == nil {
		t.Fatal("expected file:// to be refused")
	}
}

func TestValidateURLRefusesLiteralPrivateIP(t *testing.T) {
	// The cloud-metadata address specifically, as a literal IP host —
	// exactly the SSRF payload shape real-world attacks use.
	err := ValidateURL("https://169.254.169.254/latest/meta-data/", []string{"https"}, fakeResolver(nil))
	if err == nil {
		t.Fatal("expected the cloud-metadata literal address to be refused")
	}
}

func TestValidateURLRefusesLiteralLoopback(t *testing.T) {
	err := ValidateURL("https://127.0.0.1:8080/admin", []string{"https"}, fakeResolver(nil))
	if err == nil {
		t.Fatal("expected a literal loopback address to be refused")
	}
}

func TestValidateURLRefusesHostnameResolvingToPrivateAddress(t *testing.T) {
	resolver := fakeResolver(map[string][]net.IP{
		"internal.attacker-controlled.example": {net.ParseIP("10.0.0.5")},
	})
	err := ValidateURL("https://internal.attacker-controlled.example/", []string{"https"}, resolver)
	if err == nil {
		t.Fatal("expected a hostname resolving to a private address to be refused")
	}
}

func TestValidateURLRefusesWhenAnyResolvedAddressIsBlocked(t *testing.T) {
	// DNS rebinding / multi-answer shape: one legitimate public address
	// alongside one blocked address. The guard must refuse on ANY
	// blocked answer, not just the first.
	resolver := fakeResolver(map[string][]net.IP{
		"mixed.example": {net.ParseIP("8.8.8.8"), net.ParseIP("127.0.0.1")},
	})
	err := ValidateURL("https://mixed.example/", []string{"https"}, resolver)
	if err == nil {
		t.Fatal("expected refusal when ANY resolved address is blocked, not just the first")
	}
}

func TestValidateURLAcceptsGenuinePublicAddress(t *testing.T) {
	resolver := fakeResolver(map[string][]net.IP{
		"stream.aisstream.io": {net.ParseIP("203.0.113.10")},
	})
	if err := ValidateURL("wss://stream.aisstream.io/v0/stream", []string{"wss", "https"}, resolver); err != nil {
		t.Fatalf("expected a genuine public wss:// URL to be accepted, got %v", err)
	}
}

func TestValidateURLRefusesEmptyHost(t *testing.T) {
	if err := ValidateURL("https:///no-host", []string{"https"}, fakeResolver(nil)); err != ErrEmptyHost {
		t.Fatalf("expected ErrEmptyHost, got %v", err)
	}
}

func TestValidateURLRefusesWhenResolverReturnsNoAddresses(t *testing.T) {
	err := ValidateURL("https://nowhere.example/", []string{"https"}, fakeResolver(nil))
	if err == nil {
		t.Fatal("expected refusal when the resolver returns zero addresses")
	}
}
