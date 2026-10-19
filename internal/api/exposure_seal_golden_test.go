package api

import "testing"

// TestSealFormatIsPinned fixes the bytes the seal digests: for each key in
// sorted order, the key, a NUL, the stored record, and a newline. The expected
// digests were computed outside Go, with printf and shasum over exactly that
// byte layout, so every other test that checks the seal with sealOfRaw itself
// cannot hide a change to it. A change to the layout would make every existing
// database read as lost on upgrade (marvel#956 review).
func TestSealFormatIsPinned(t *testing.T) {
	base := map[string][]byte{
		"a|1": []byte(`{"x":1}`),
		"b|2": []byte(`{"y":2}`),
	}
	cases := []struct {
		name   string
		recs   map[string][]byte
		key    string
		with   []byte
		count  int
		digest string
	}{
		{"two records", base, "", nil, 2, "3e8c382890ae810350cf0309f94f376ec3203b440e825d4f71bde61b8e8ab3e2"},
		{"one replaced", base, "a|1", []byte(`{"x":9}`), 2, "4a5736019a52f7ba793fd16244ee3bcfa10f32468ce6d8996a4a92cfd6c2b942"},
		{"one added", base, "c|3", []byte(`{"z":3}`), 3, "aa1eeffc775465212f256b70d3f5d85e1966c64318c41de73be79c27cdf06dc1"},
		{"none", map[string][]byte{}, "", nil, 0, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	}
	for _, tc := range cases {
		got := sealOfRaw(tc.recs, tc.key, tc.with)
		if got.Count != tc.count || got.Digest != tc.digest {
			t.Errorf("%s: seal = %d %s, want %d %s", tc.name, got.Count, got.Digest, tc.count, tc.digest)
		}
	}
}
