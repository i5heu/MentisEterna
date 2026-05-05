package db

import (
	"database/sql"
	"encoding/binary"
	"errors"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// WebAuthnCredentialRow mirrors a row in the webauthn_credentials table.
type WebAuthnCredentialRow struct {
	ID              int64
	UserID          int64
	CredentialID    []byte
	PublicKey       []byte
	AttestationType string
	SignCount       uint32
}

// InsertWebAuthnCredential persists a webauthn.Credential for a given user.
func (d *DB) InsertWebAuthnCredential(userID int64, cred *webauthn.Credential) error {
	_, err := d.Exec(
		`INSERT INTO webauthn_credentials (user_id, credential_id, public_key, attestation_type, attestation_format, flags, sign_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID,
		cred.ID,
		cred.PublicKey,
		cred.AttestationType,
		cred.AttestationFormat,
		int(cred.Flags.ProtocolValue()),
		cred.Authenticator.SignCount,
	)
	return err
}

// GetWebAuthnCredentials returns all stored credentials for a user, converted to webauthn.Credential objects.
func (d *DB) GetWebAuthnCredentials(userID int64) ([]webauthn.Credential, error) {
	rows, err := d.Query(
		`SELECT credential_id, public_key, attestation_type, attestation_format, flags, sign_count
		 FROM webauthn_credentials WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var creds []webauthn.Credential
	for rows.Next() {
		var c webauthn.Credential
		var flagsRaw int
		if err := rows.Scan(&c.ID, &c.PublicKey, &c.AttestationType, &c.AttestationFormat, &flagsRaw, &c.Authenticator.SignCount); err != nil {
			return nil, err
		}
		c.Flags = webauthn.NewCredentialFlags(protocol.AuthenticatorFlags(flagsRaw))
		creds = append(creds, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return creds, nil
}

// GetUserByWebAuthnCredential finds the user who owns a credential with the given credential ID.
func (d *DB) GetUserByWebAuthnCredential(credentialID []byte) (userID int64, username string, err error) {
	err = d.QueryRow(
		`SELECT wc.user_id, a.username
		 FROM webauthn_credentials wc
		 JOIN auth a ON a.id = wc.user_id
		 WHERE wc.credential_id = ?`, credentialID,
	).Scan(&userID, &username)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	return
}

// UpdateWebAuthnSignCount updates the stored sign counter and flags after a successful assertion.
func (d *DB) UpdateWebAuthnSignCount(credentialID []byte, signCount uint32, flags webauthn.CredentialFlags) error {
	_, err := d.Exec(
		`UPDATE webauthn_credentials SET sign_count = ?, flags = ? WHERE credential_id = ?`,
		signCount, int(flags.ProtocolValue()), credentialID,
	)
	return err
}

// Int64ToUserHandle converts an int64 user ID to the byte slice format suitable for WebAuthnID().
func Int64ToUserHandle(id int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(id))
	return buf
}

// UserHandleToInt64 converts a WebAuthn user handle back to an int64 user ID.
func UserHandleToInt64(handle []byte) (int64, error) {
	if len(handle) < 8 {
		return 0, errors.New("user handle too short")
	}
	return int64(binary.BigEndian.Uint64(handle[:8])), nil
}
