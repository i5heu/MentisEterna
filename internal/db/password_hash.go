package db

import (
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argon2Memory      = 64 * 1024
	argon2Iterations  = 3
	argon2Parallelism = 2
	argon2KeyLength   = 32
	argon2SaltLength  = 16
)

func hashPassword(plaintext string) (string, error) {
	salt, err := randomBytes(argon2SaltLength)
	if err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(plaintext), salt, argon2Iterations, argon2Memory, argon2Parallelism, argon2KeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argon2Memory,
		argon2Iterations,
		argon2Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func verifyPasswordHash(stored, plaintext string) (bool, bool, error) {
	if isLegacySHA512Hash(stored) {
		sum := sha512.Sum512([]byte(plaintext))
		legacy := fmt.Sprintf("%x", sum)
		return subtle.ConstantTimeCompare([]byte(legacy), []byte(stored)) == 1, true, nil
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, fmt.Errorf("unsupported password hash format")
	}
	if parts[2] != "v=19" {
		return false, false, fmt.Errorf("unsupported argon2 version %q", parts[2])
	}

	params, err := parseArgon2Params(parts[3])
	if err != nil {
		return false, false, err
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, false, fmt.Errorf("decode argon2 salt: %w", err)
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, false, fmt.Errorf("decode argon2 hash: %w", err)
	}
	actual := argon2.IDKey([]byte(plaintext), salt, params.iterations, params.memory, params.parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(expected, actual) == 1, false, nil
}

type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parseArgon2Params(raw string) (argon2Params, error) {
	var params argon2Params
	for _, part := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			return argon2Params{}, fmt.Errorf("invalid argon2 params %q", raw)
		}
		switch kv[0] {
		case "m":
			n, err := strconv.ParseUint(kv[1], 10, 32)
			if err != nil {
				return argon2Params{}, fmt.Errorf("parse argon2 memory: %w", err)
			}
			params.memory = uint32(n)
		case "t":
			n, err := strconv.ParseUint(kv[1], 10, 32)
			if err != nil {
				return argon2Params{}, fmt.Errorf("parse argon2 iterations: %w", err)
			}
			params.iterations = uint32(n)
		case "p":
			n, err := strconv.ParseUint(kv[1], 10, 8)
			if err != nil {
				return argon2Params{}, fmt.Errorf("parse argon2 parallelism: %w", err)
			}
			params.parallelism = uint8(n)
		}
	}
	if params.memory == 0 || params.iterations == 0 || params.parallelism == 0 {
		return argon2Params{}, fmt.Errorf("invalid argon2 params %q", raw)
	}
	return params, nil
}

func isLegacySHA512Hash(stored string) bool {
	if len(stored) != 128 {
		return false
	}
	for _, c := range stored {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func randomBytes(size int) ([]byte, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}
