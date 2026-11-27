package envelope_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core/cfb"
	"github.com/AOShei/go-fast-core/cfb/cfbtest"
	"github.com/AOShei/go-fast-core/envelope"
)

func TestDetect(t *testing.T) {
	x := []byte("x")
	for _, c := range []struct {
		what    string
		streams map[string][]byte
		want    envelope.Protection
	}{
		{"Office's rights management, data space", map[string][]byte{"\x06DataSpaces/DataSpaceInfo/\x09DRMEncryptedDataSpace": x, "EncryptedPackage": x}, envelope.RightsManaged},
		{"Office's rights management, transform", map[string][]byte{"\x06DataSpaces/TransformInfo/\x09DRMEncryptedTransform": x}, envelope.RightsManaged},
		{"rights-managed content", map[string][]byte{"\x09DRMContent": x}, envelope.RightsManaged},
		{"the .NET classes' envelope around XPS", map[string][]byte{"\x06DataSpaces/TransformInfo/EncryptionTransform/EUL-0123": x, "EncryptedPackage": x}, envelope.RightsManaged},
		{"a password", map[string][]byte{"EncryptionInfo": x, "EncryptedPackage": x, "\x06DataSpaces/Version": x}, envelope.Password},
		{"a data space alone", map[string][]byte{"\x06DataSpaces/Version": x}, envelope.Unstated},
		{"an encrypted package alone", map[string][]byte{"EncryptedPackage": x}, envelope.Unstated},
		{"a name like a licence elsewhere", map[string][]byte{"\x06DataSpaces/DataSpaceMap": x, "EUL-1": x}, envelope.Unstated},
		{"a Word 97 file", map[string][]byte{"WordDocument": x, "1Table": x}, envelope.None},
	} {
		data := cfbtest.Compound(c.streams)
		f, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if got := envelope.Detect(f); got != c.want {
			t.Errorf("%s: %v, want %v", c.what, got, c.want)
		}
	}
}

func TestMessage(t *testing.T) {
	if m := envelope.Message(envelope.None, "package"); m != "" {
		t.Errorf("None: %q", m)
	}
	for _, p := range []envelope.Protection{envelope.RightsManaged, envelope.Password, envelope.Unstated} {
		for _, subject := range []string{"Office document", "package"} {
			if m := envelope.Message(p, subject); !strings.HasPrefix(m, "encrypted "+subject+" ") {
				t.Errorf("%v, %s: %q", p, subject, m)
			}
		}
	}
	// go-fast-ooxml's wording before the move, kept.
	if m := envelope.Message(envelope.RightsManaged, "Office document"); m != "encrypted Office document protected by rights management (Information Rights Management): "+
		"its key is in a licence that a rights-management server issues to the people the document names, and this library contacts no server" {
		t.Errorf("rights managed: %q", m)
	}
}
