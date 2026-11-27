// Package envelope says what protects an encrypted package: the compound
// file that an encrypted Office document is stored in ([MS-OFFCRYPTO]),
// and that an XPS document under rights management is stored in too. It
// is one test for go-fast-ooxml, go-fast-pdf and go-fast-msbin, and one
// wording of what each library says of a package it does not open.
//
// Nothing is decrypted here. A library that decrypts (go-fast-ooxml, with
// a password or a private key) does so after Detect says Password.
package envelope

import (
	"strings"

	"github.com/AOShei/go-fast-core/cfb"
)

// Protection is what an envelope says protects the package in it.
type Protection int

const (
	// None: the compound file is no envelope.
	None Protection = iota
	// RightsManaged: rights management (Information Rights Management).
	// The key is in a licence that a rights-management server issues to
	// the people the document names ([MS-OFFCRYPTO] 2.2.11, 2.6).
	RightsManaged
	// Password: the EncryptionInfo and EncryptedPackage streams of an
	// encrypted Office document. The key is made from a password, or held
	// for the owners of certificates ([MS-OFFCRYPTO] 2.3).
	Password
	// Unstated: an envelope (a \x06DataSpaces storage or an
	// EncryptedPackage stream) that is neither of the others.
	Unstated
)

// Detect says what protects the package f holds. Rights management is
// known by the streams Office writes (\x09DRMEncryptedDataSpace,
// \x09DRMEncryptedTransform, \x09DRMContent) and by those the .NET
// packaging classes write around an XPS document, which name the
// transform as they like and keep each user's licence beside it, in a
// stream whose name begins "EUL-".
func Detect(f *cfb.File) Protection {
	switch {
	case rightsManaged(f):
		return RightsManaged
	case f.Has("EncryptionInfo") && f.Has("EncryptedPackage"):
		return Password
	case f.Has("\x06DataSpaces") || f.Has("EncryptedPackage"):
		return Unstated
	}
	return None
}

func rightsManaged(f *cfb.File) bool {
	if f.Has("\x06DataSpaces", "DataSpaceInfo", "\x09DRMEncryptedDataSpace") ||
		f.Has("\x06DataSpaces", "TransformInfo", "\x09DRMEncryptedTransform") || f.Has("\x09DRMContent") {
		return true
	}
	for _, t := range f.List("\x06DataSpaces", "TransformInfo") {
		for _, name := range f.List("\x06DataSpaces", "TransformInfo", t) {
			if strings.HasPrefix(name, "EUL-") {
				return true
			}
		}
	}
	return false
}

// Message says what a package that is not opened is, for the text of an
// error; subject names it: "Office document", "package". It is "" for
// None.
func Message(p Protection, subject string) string {
	switch p {
	case RightsManaged:
		return "encrypted " + subject + " protected by rights management (Information Rights Management): " +
			"its key is in a licence that a rights-management server issues to the people the document names, " +
			"and this library contacts no server"
	case Password:
		return "encrypted " + subject + " protected with a password or for the owners of certificates, " +
			"in the envelope of an encrypted Office document: this library does not decrypt it"
	case Unstated:
		return "encrypted " + subject + " that is not protected with a password, and whose encryption is not stated"
	}
	return ""
}
