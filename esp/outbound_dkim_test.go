package main

import (
	"strings"
	"testing"
)

// Phase 60: real tests for the pure message-building/address-parsing
// helpers outbound mail depends on.

func TestBuildRawMessage_ContainsRealHeadersAndBody(t *testing.T) {
	raw := buildRawMessage("jane@example.com", "bob@other.com", "Hello", "This is the body.", "msg123")
	s := string(raw)

	for _, want := range []string{
		"From: jane@example.com",
		"To: bob@other.com",
		"Subject: Hello",
		"This is the body.",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("expected raw message to contain %q, got:\n%s", want, s)
		}
	}
}

func TestBuildRawMessage_MessageIDUsesFromDomain(t *testing.T) {
	raw := buildRawMessage("jane@example.com", "bob@other.com", "Hello", "body", "msg123")
	s := string(raw)
	if !strings.Contains(s, "msg123@example.com") {
		t.Errorf("expected Message-Id to use the From address's domain (example.com), got:\n%s", s)
	}
}

func TestBuildRawMessage_NoMessageIDWhenEmpty(t *testing.T) {
	raw := buildRawMessage("jane@example.com", "bob@other.com", "Hello", "body", "")
	if strings.Contains(string(raw), "Message-Id:") {
		t.Errorf("expected no Message-Id header when messageID is empty")
	}
}

func TestAddressOnly_ExtractsBareAddressFromDisplayName(t *testing.T) {
	cases := map[string]string{
		"Jane Doe <jane@example.com>": "jane@example.com",
		"jane@example.com":            "jane@example.com",
		"<jane@example.com>":          "jane@example.com",
	}
	for input, want := range cases {
		got := addressOnly(input)
		if got != want {
			t.Errorf("addressOnly(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDomainPartOf_ExtractsDomain(t *testing.T) {
	cases := map[string]string{
		"jane@example.com": "example.com",
		"JANE@EXAMPLE.COM": "example.com",
		"not-an-email":     "",
		"trailing@":        "",
	}
	for input, want := range cases {
		got := domainPartOf(input)
		if got != want {
			t.Errorf("domainPartOf(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAddressOnlyThenDomainPartOf_HandlesDisplayNameFormat(t *testing.T) {
	// This is the real bug Phase 55 fixed in signOutboundMessage/buildRawMessage:
	// domainPartOf alone on a "Name <addr@domain>" string would grab
	// "domain>" (with the trailing bracket) instead of "domain".
	got := domainPartOf(addressOnly("Jane Doe <jane@example.com>"))
	if got != "example.com" {
		t.Errorf("expected domainPartOf(addressOnly(...)) = %q, got %q", "example.com", got)
	}
}
