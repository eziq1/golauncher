// Package cred keeps secrets in the Windows Credential Manager as generic
// credentials of the local machine's user profile (CRED_TYPE_GENERIC,
// CRED_PERSIST_LOCAL_MACHINE). A secret is stored as UTF-16 text, as the
// Credential Manager UI and other tools expect.
//
// On macOS a secret is a generic password of the login keychain instead
// (cred_darwin.go). Every function may be called from any goroutine. On
// other platforms they return errors.ErrUnsupported.
package cred

// MaxSecretLen is the longest secret, in UTF-16 code units, that the
// Credential Manager stores (CRED_MAX_CREDENTIAL_BLOB_SIZE is 2560 bytes).
const MaxSecretLen = 2560 / 2
