package s4a21

import "testing"

func TestStorePolicyClassifiesDisjointFailures(t *testing.T) {
	policy := StorePolicy{}
	if policy.DisjointStoreCost() != 500 {
		t.Fatalf("disjoint store cost = %d, want 500", policy.DisjointStoreCost())
	}
	retryable := []byte{0x52, 0xbe}
	for _, code := range retryable {
		reason, retry := policy.DisjointFailure(code)
		if !retry {
			t.Fatalf("code 0x%02x should retry in the same session", code)
		}
		if verified, known := policy.DisjointReasonRetryable(reason); !known || !verified {
			t.Fatalf("reason %q should be retryable and adapter-owned", reason)
		}
	}
	terminal := []byte{0x0a, 0x13, 0x15, 0x16, 0xff, 0xfe}
	for _, code := range terminal {
		reason, retry := policy.DisjointFailure(code)
		if retry {
			t.Fatalf("code 0x%02x must stop the current robot", code)
		}
		if verified, known := policy.DisjointReasonRetryable(reason); !known || verified {
			t.Fatalf("reason %q should be known and terminal", reason)
		}
	}
	if reason, retry := policy.DisjointFailure(0); reason != "disjoint_failed" || retry {
		t.Fatalf("zero code = %q retry=%t", reason, retry)
	}
	if _, known := policy.DisjointReasonRetryable("set_area_failed"); known {
		t.Fatal("scheduler-owned reasons must not be claimed by the adapter policy")
	}
}

func TestStorePolicyClassifiesEnchantFailures(t *testing.T) {
	policy := StorePolicy{}
	if policy.EnchantStoreCost() != 500 {
		t.Fatalf("enchant store cost = %d, want 500", policy.EnchantStoreCost())
	}
	for _, code := range []byte{0x52, 0xbe} {
		reason, retry := policy.EnchantFailure(code)
		if !retry {
			t.Fatalf("enchant code 0x%02x should retry in the same session", code)
		}
		if verified, known := policy.EnchantReasonRetryable(reason); !known || !verified {
			t.Fatalf("enchant reason %q should be retryable and adapter-owned", reason)
		}
	}
	for _, code := range []byte{0x0a, 0x13, 0x15, 0xff} {
		reason, retry := policy.EnchantFailure(code)
		if retry {
			t.Fatalf("enchant code 0x%02x must stop the current robot", code)
		}
		if verified, known := policy.EnchantReasonRetryable(reason); !known || verified {
			t.Fatalf("enchant reason %q should be known and terminal", reason)
		}
	}
	if reason, retry := policy.EnchantFailure(0); reason != "enchant_failed" || retry {
		t.Fatalf("enchant zero code = %q retry=%t", reason, retry)
	}
	if _, known := policy.EnchantReasonRetryable("set_area_failed"); known {
		t.Fatal("scheduler-owned reasons must not be claimed by the enchant policy")
	}
}
