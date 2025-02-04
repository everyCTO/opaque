// SPDX-License-Identifier: MIT
//
// Copyright (C) 2021 Daniel Bourdrez. All Rights Reserved.
//
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree or at
// https://spdx.org/licenses/MIT.html

// Package opaque implements the OPAQUE asymmetric password-authenticated key exchange protocol.
//
// OPAQUE is an asymmetric Password Authenticated Key Exchange (PAKE).
//
// This package implements the official OPAQUE definition. For protocol details, please refer to the IETF protocol
// document at https://datatracker.ietf.org/doc/draft-irtf-cfrg-opaque.
//
package opaque

import (
	"github.com/bytemare/cryptotools/group/ciphersuite"
	"github.com/bytemare/cryptotools/hash"
	"github.com/bytemare/cryptotools/mhf"

	"github.com/bytemare/opaque/internal"
	"github.com/bytemare/opaque/internal/encoding"
	"github.com/bytemare/opaque/internal/oprf"
	"github.com/bytemare/opaque/message"
)

// Mode designates OPAQUE's envelope mode.
type Mode byte

const (
	// Internal designates the internal mode.
	Internal Mode = iota + 1

	// External designates the external mode.
	External
)

// Group identifies the prime-order group with hash-to-curve capability to use in OPRF and AKE.
type Group byte

const (
	// RistrettoSha512 identifies the Ristretto255 group and SHA-512.
	RistrettoSha512 = Group(oprf.RistrettoSha512)

	// decaf448Shake256 identifies the Decaf448 group and Shake-256.
	// decaf448Shake256 = 2.

	// P256Sha256 identifies the NIST P-256 group and SHA-256.
	P256Sha256 = Group(oprf.P256Sha256)

	// P384Sha512 identifies the NIST P-384 group and SHA-512.
	P384Sha512 = Group(oprf.P384Sha512)

	// P521Sha512 identifies the NIST P-512 group and SHA-512.
	P521Sha512 = Group(oprf.P521Sha512)

	confLength = 7
)

// Credentials holds the client and server ids (will certainly disappear in next versions°.
type Credentials struct {
	Client, Server              []byte
	TestEnvNonce, TestMaskNonce []byte
}

// Configuration represents an OPAQUE configuration. Note that OprfGroup and AKEGroup are recommended to be the same,
// as well as KDF, MAC, Hash should be the same.
type Configuration struct {
	// Group identifies the group and ciphersuite to use for the OPRF and AKE.
	Group Group `json:"oprf"`

	// KDF identifies the hash function to be used for key derivation (e.g. HKDF).
	// Identifiers are defined in github.com/bytemare/cryptotools/hash.
	KDF hash.Hashing `json:"kdf"`

	// MAC identifies the hash function to be used for message authentication (e.g. HMAC).
	// Identifiers are defined in github.com/bytemare/cryptotools/hash.
	MAC hash.Hashing `json:"mac"`

	// Hash identifies the hash function to be used for hashing, as defined in github.com/bytemare/cryptotools/hash.
	Hash hash.Hashing `json:"hash"`

	// MHF identifies the memory-hard function for expensive key derivation on the client,
	// defined in github.com/bytemare/cryptotools/mhf.
	MHF mhf.Identifier `json:"mhf"`

	// Mode identifies the envelope mode to be used.
	Mode Mode `json:"mode"`

	// Context is optional shared information to include in the AKE transcript.
	Context []byte

	// NonceLen identifies the length to use for nonces. 32 is the recommended value.
	NonceLen int `json:"nn"`
}

func envelopeSize(mode Mode, p *internal.Parameters) int {
	innerSize := 0
	if mode == External {
		innerSize = encoding.ScalarLength[p.Group]
	}

	return p.NonceLen + p.MAC.Size() + innerSize
}

func (c *Configuration) toInternal() *internal.Parameters {
	g := ciphersuite.Identifier(c.Group)
	ip := &internal.Parameters{
		KDF:             &internal.KDF{H: c.KDF.Get()},
		MAC:             &internal.Mac{H: c.MAC.Get()},
		Hash:            &internal.Hash{H: c.Hash.Get()},
		MHF:             &internal.MHF{MHF: c.MHF.Get()},
		NonceLen:        c.NonceLen,
		OPRFPointLength: encoding.PointLength[g],
		AkePointLength:  encoding.PointLength[g],
		Group:           g,
		OPRF:            oprf.Ciphersuite(g),
		Context:         c.Context,
	}
	ip.EnvelopeSize = envelopeSize(c.Mode, ip)

	return ip
}

// Serialize returns the byte encoding of the Configuration structure.
func (c *Configuration) Serialize() []byte {
	b := make([]byte, confLength)
	b[0] = byte(c.Group)
	b[1] = byte(c.KDF)
	b[2] = byte(c.MAC)
	b[3] = byte(c.Hash)
	b[4] = byte(c.MHF)
	b[5] = byte(c.Mode)
	b[6] = encoding.I2OSP(c.NonceLen, 1)[0]

	return b
}

// Client returns a newly instantiated Client from the Configuration.
func (c *Configuration) Client() *Client {
	return NewClient(c)
}

// Server returns a newly instantiated Server from the Configuration.
func (c *Configuration) Server() *Server {
	return NewServer(c)
}

// DeserializeConfiguration decodes the input and returns a Parameter structure. This assumes that the encoded parameters
// are valid, and will not be checked.
func DeserializeConfiguration(encoded []byte) (*Configuration, error) {
	if len(encoded) != confLength {
		return nil, internal.ErrConfigurationInvalidLength
	}

	return &Configuration{
		Group:    Group(encoded[0]),
		KDF:      hash.Hashing(encoded[1]),
		MAC:      hash.Hashing(encoded[2]),
		Hash:     hash.Hashing(encoded[3]),
		MHF:      mhf.Identifier(encoded[4]),
		Mode:     Mode(encoded[5]),
		NonceLen: encoding.OS2IP(encoded[6:]),
	}, nil
}

// DefaultConfiguration returns a default configuration with strong parameters.
func DefaultConfiguration() *Configuration {
	return &Configuration{
		Group:    RistrettoSha512,
		KDF:      hash.SHA512,
		MAC:      hash.SHA512,
		Hash:     hash.SHA512,
		MHF:      mhf.Scrypt,
		Mode:     Internal,
		NonceLen: 32,
	}
}

// ClientRecord is a server-side structure enabling the storage of user relevant information.
type ClientRecord struct {
	CredentialIdentifier []byte
	ClientIdentity       []byte
	*message.RegistrationUpload

	// testing
	TestMaskNonce []byte
}

// GetFakeEnvelope returns a byte array filled with 0s the length of a legitimate envelope size in the configuration's mode.
// This fake envelope byte array is used in the client enumeration mitigation scheme.
func GetFakeEnvelope(c *Configuration) []byte {
	l := c.toInternal().EnvelopeSize
	return make([]byte, l)
}
