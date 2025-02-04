// SPDX-License-Identifier: MIT
//
// Copyright (C) 2021 Daniel Bourdrez. All Rights Reserved.
//
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree or at
// https://spdx.org/licenses/MIT.html

// Package ake provides high-level functions for the 3DH AKE.
package ake

import (
	"fmt"

	"github.com/bytemare/cryptotools/group"
	"github.com/bytemare/cryptotools/group/ciphersuite"

	"github.com/bytemare/opaque/internal"
	"github.com/bytemare/opaque/internal/encoding"
	"github.com/bytemare/opaque/internal/tag"
	"github.com/bytemare/opaque/message"
)

type selector bool

const (
	client selector = true
	server selector = false
)

// KeyGen returns private and public keys in the group.
func KeyGen(id ciphersuite.Identifier) (sk, pk []byte) {
	scalar := id.NewScalar().Random()
	publicKey := id.Base().Mult(scalar)

	return encoding.SerializeScalar(scalar, id), encoding.SerializePoint(publicKey, id)
}

// setValues - testing: integrated to support testing, to force values.
// There's no effect if esk, epk, and nonce have already been set in a previous call.
func setValues(g group.Group, scalar group.Scalar, nonce []byte, nonceLen int) (s group.Scalar, n []byte) {
	if scalar != nil {
		s = scalar
	} else {
		s = g.NewScalar().Random()
	}

	if len(nonce) == 0 {
		nonce = internal.RandomBytes(nonceLen)
	}

	return s, nonce
}

func buildLabel(length int, label, context []byte) []byte {
	return encoding.Concat3(
		encoding.I2OSP(length, 2),
		encoding.EncodeVectorLen(append([]byte(tag.LabelPrefix), label...), 1),
		encoding.EncodeVectorLen(context, 1))
}

func expand(h *internal.KDF, secret, hkdfLabel []byte) []byte {
	return h.Expand(secret, hkdfLabel, h.Size())
}

func expandLabel(h *internal.KDF, secret, label, context []byte) []byte {
	hkdfLabel := buildLabel(h.Size(), label, context)
	return expand(h, secret, hkdfLabel)
}

func deriveSecret(h *internal.KDF, secret, label, context []byte) []byte {
	return expandLabel(h, secret, label, context)
}

func initTranscript(p *internal.Parameters, idc, ids []byte, ke1 *message.KE1, ke2 *message.KE2) {
	sidc := encoding.EncodeVector(idc)
	sids := encoding.EncodeVector(ids)
	p.Hash.Write(encoding.Concatenate([]byte(tag.VersionTag), encoding.EncodeVector(p.Context),
		sidc, ke1.Serialize(),
		sids, ke2.CredentialResponse.Serialize(), ke2.NonceS, ke2.EpkS))
}

type macKeys struct {
	serverMacKey, clientMacKey []byte
}

func deriveKeys(h *internal.KDF, ikm, context []byte) (k *macKeys, sessionSecret []byte) {
	prk := h.Extract(nil, ikm)
	k = &macKeys{}
	handshakeSecret := deriveSecret(h, prk, []byte(tag.Handshake), context)
	sessionSecret = deriveSecret(h, prk, []byte(tag.Session), context)
	k.serverMacKey = expandLabel(h, handshakeSecret, []byte(tag.MacServer), nil)
	k.clientMacKey = expandLabel(h, handshakeSecret, []byte(tag.MacClient), nil)

	return k, sessionSecret
}

func decodeKeys(g group.Group, peerEpk, peerPk []byte) (epk, pk group.Element, err error) {
	epk, err = g.NewElement().Decode(peerEpk)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding peer ephemeral public key: %w", err)
	}

	pk, err = g.NewElement().Decode(peerPk)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding peer public key: %w", err)
	}

	return epk, pk, nil
}

func k3dh(p1 group.Element, s1 group.Scalar, p2 group.Element, s2 group.Scalar, p3 group.Element, s3 group.Scalar) []byte {
	e1 := p1.Mult(s1)
	e2 := p2.Mult(s2)
	e3 := p3.Mult(s3)

	return encoding.Concat3(e1.Bytes(), e2.Bytes(), e3.Bytes())
}

func ikm(s selector, g group.Group, esk, secretKey group.Scalar, peerEpk, peerPublicKey []byte) ([]byte, error) {
	epk, gpk, err := decodeKeys(g, peerEpk, peerPublicKey)
	if err != nil {
		return nil, err
	}

	switch s {
	case client:
		return k3dh(epk, esk, gpk, esk, epk, secretKey), nil
	default: // server
		return k3dh(epk, esk, epk, secretKey, gpk, esk), nil
	}
}

type macs struct {
	serverMac, clientMac []byte
}

type coreKeys struct {
	esk, secretKey         group.Scalar
	peerEpk, peerPublicKey []byte
}

func core3DH(s selector, p *internal.Parameters, k *coreKeys, idu, ids []byte,
	ke1 *message.KE1, ke2 *message.KE2) (*macs, []byte, error) {
	ikm, err := ikm(s, p.Group, k.esk, k.secretKey, k.peerEpk, k.peerPublicKey)
	if err != nil {
		return nil, nil, err
	}

	initTranscript(p, idu, ids, ke1, ke2)
	keys, sessionSecret := deriveKeys(p.KDF, ikm, p.Hash.Sum()) // preamble
	m := &macs{
		serverMac: p.MAC.MAC(keys.serverMacKey, p.Hash.Sum()), // transcript2
	}
	p.Hash.Write(m.serverMac)
	transcript3 := p.Hash.Sum()
	m.clientMac = p.MAC.MAC(keys.clientMacKey, transcript3)

	return m, sessionSecret, nil
}
