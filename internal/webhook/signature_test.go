package webhook

import "testing"

// Vectors from RFC 4231, section 4.
func TestVerify(t *testing.T) {
	rfcKey1 := []byte("\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b")
	rfcSig1 := "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"
	rfcSig2 := "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"

	tests := []struct {
		name   string
		secret []byte
		body   []byte
		header string
		want   bool
	}{
		{
			name:   "rfc 4231 case 1",
			secret: rfcKey1,
			body:   []byte("Hi There"),
			header: rfcSig1,
			want:   true,
		},
		{
			name:   "rfc 4231 case 2",
			secret: []byte("Jefe"),
			body:   []byte("what do ya want for nothing?"),
			header: rfcSig2,
			want:   true,
		},
		{
			name:   "uppercase hex accepted",
			secret: []byte("Jefe"),
			body:   []byte("what do ya want for nothing?"),
			header: "5BDCC146BF60754E6A042426089575C75A003F089D2739839DEC58B964EC3843",
			want:   true,
		},
		{
			name:   "wrong secret",
			secret: []byte("Jefe2"),
			body:   []byte("what do ya want for nothing?"),
			header: rfcSig2,
			want:   false,
		},
		{
			name:   "tampered body",
			secret: []byte("Jefe"),
			body:   []byte("what do ya want for nothing!"),
			header: rfcSig2,
			want:   false,
		},
		{
			name:   "truncated signature",
			secret: []byte("Jefe"),
			body:   []byte("what do ya want for nothing?"),
			header: rfcSig2[:len(rfcSig2)-2],
			want:   false,
		},
		{
			name:   "non-hex header",
			secret: []byte("Jefe"),
			body:   []byte("what do ya want for nothing?"),
			header: "not-hex",
			want:   false,
		},
		{
			name:   "missing header",
			secret: []byte("Jefe"),
			body:   []byte("what do ya want for nothing?"),
			header: "",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Verify(tt.secret, tt.body, tt.header); got != tt.want {
				t.Fatalf("Verify() = %v, want %v", got, tt.want)
			}
		})
	}
}
