package nexserver

import (
	"encoding/binary"
	"testing"
)

// buildString encodes a NEX String: UInt16 length including the terminator,
// then the bytes, then a null.
func buildString(value string) []byte {
	out := make([]byte, 2, 2+len(value)+1)
	binary.LittleEndian.PutUint16(out, uint16(len(value)+1))
	out = append(out, value...)
	return append(out, 0)
}

// buildAuthenticationInfo encodes the structure a Wii U sends as login data:
// String token, UInt32 ngsVersion, UInt8 tokenType, UInt32 serverVersion.
func buildAuthenticationInfo(token string) []byte {
	out := buildString(token)
	out = binary.LittleEndian.AppendUint32(out, 3)
	out = append(out, 1)
	return binary.LittleEndian.AppendUint32(out, 3)
}

// withHeaders wraps content in a structure header chain. AuthenticationInfo
// derives from Data, so a real client emits two: an empty one for Data, then
// one for AuthenticationInfo itself.
func withHeaders(content []byte) []byte {
	out := []byte{0}
	out = binary.LittleEndian.AppendUint32(out, 0) // * Data: version 0, no fields

	out = append(out, 0)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(content)))

	return append(out, content...)
}

// buildDataHolder encodes a DataHolder around an already-encoded object.
func buildDataHolder(typeName string, object []byte) []byte {
	out := buildString(typeName)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(object)+4))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(object)))
	return append(out, object...)
}

func TestObjectHasStructureHeader(t *testing.T) {
	tests := []struct {
		name       string
		object     []byte
		wantHeader bool
		wantOK     bool
	}{
		{
			name:       "header chain matching the object exactly",
			object:     withHeaders(buildAuthenticationInfo("e2e-test-token")),
			wantHeader: true,
			wantOK:     true,
		},
		{
			name:       "bare structure starting with a String",
			object:     buildAuthenticationInfo("e2e-test-token"),
			wantHeader: false,
			wantOK:     true,
		},
		{
			name:       "bare structure with a short token",
			object:     buildAuthenticationInfo("ab"),
			wantHeader: false,
			wantOK:     true,
		},
		{
			name:       "bare structure with a long realistic NNAS token",
			object:     buildAuthenticationInfo("bWFpbjpodHRwczovL2FjY291bnQubmludGVuZG8ubmV0L3YxL2FwaQ"),
			wantHeader: false,
			wantOK:     true,
		},
		{
			name:       "header chain around an empty structure",
			object:     withHeaders(nil),
			wantHeader: true,
			wantOK:     true,
		},
		{
			name:   "empty object is undecidable",
			object: nil,
			wantOK: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotHeader, gotOK := objectHasStructureHeader(test.object)

			if gotOK != test.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, test.wantOK)
			}

			if gotOK && gotHeader != test.wantHeader {
				t.Fatalf("header = %v, want %v", gotHeader, test.wantHeader)
			}
		})
	}
}

func TestLoginDataObject(t *testing.T) {
	object := withHeaders(buildAuthenticationInfo("token"))

	// * TicketGranting::LoginEx(String strUserName, DataHolder oExtraData)
	loginEx := append(buildString("1800000001"), buildDataHolder("AuthenticationInfo", object)...)

	got, ok := loginDataObject(0x0A, 0x02, loginEx)
	if !ok {
		t.Fatal("failed to locate the login data object in a LoginEx request")
	}

	if string(got) != string(object) {
		t.Fatalf("object = %x, want %x", got, object)
	}

	// * SecureConnection::RegisterEx(List<StationURL> vecMyURLs, DataHolder hCustomData)
	registerEx := binary.LittleEndian.AppendUint32(nil, 2)
	registerEx = append(registerEx, buildString("prudp:/address=192.168.1.50;port=12345")...)
	registerEx = append(registerEx, buildString("prudp:/address=203.0.113.9;port=12345")...)
	registerEx = append(registerEx, buildDataHolder("NintendoLoginData", object)...)

	got, ok = loginDataObject(0x0B, 0x04, registerEx)
	if !ok {
		t.Fatal("failed to locate the login data object in a RegisterEx request")
	}

	if string(got) != string(object) {
		t.Fatalf("object = %x, want %x", got, object)
	}

	// * Anything else must be ignored rather than misread.
	if _, ok := loginDataObject(0x6D, 0x06, loginEx); ok {
		t.Fatal("unrelated requests must not be treated as login data")
	}

	// * A truncated request must be rejected, not parsed past the end.
	if _, ok := loginDataObject(0x0A, 0x02, loginEx[:len(loginEx)/2]); ok {
		t.Fatal("a truncated request must not yield an object")
	}
}

// TestDetectionRoundTrip walks the whole decision for both wire formats, which
// is the case that matters: the server must reach the right answer whichever
// one the console uses.
func TestDetectionRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name   string
		object []byte
		want   bool
	}{
		{"console using structure headers", withHeaders(buildAuthenticationInfo("nex-token")), true},
		{"console without structure headers", buildAuthenticationInfo("nex-token"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			parameters := append(buildString("1800000001"), buildDataHolder("AuthenticationInfo", test.object)...)

			object, ok := loginDataObject(0x0A, 0x02, parameters)
			if !ok {
				t.Fatal("could not locate the login data object")
			}

			got, ok := objectHasStructureHeader(object)
			if !ok {
				t.Fatal("could not decide on structure header use")
			}

			if got != test.want {
				t.Fatalf("structure headers = %v, want %v", got, test.want)
			}
		})
	}
}
