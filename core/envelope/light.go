package envelope

import (
	"crypto/hmac"
	"errors"

	"github.com/bytemare/cryptotools/group"
	"github.com/bytemare/cryptotools/group/ciphersuite"
	"github.com/bytemare/cryptotools/hash"
	"github.com/bytemare/cryptotools/mhf"
	"github.com/bytemare/cryptotools/utils"
)

const OptimalMode Mode = 2

type EnvelopeOpt struct {
	AuthTag []byte
}

type Optimal struct {
	Group ciphersuite.Identifier
	Hash  hash.Hashing
	*mhf.Parameters
}

func (o *Optimal) BuildEnvelopeOptimal(unblinded, pks []byte) (skc, pkc []byte, envU *EnvelopeOpt) {
	sk, authTag := o.buildKeys(unblinded, pks)
	pk := o.Group.Get(nil).Base().Mult(sk)

	return sk.Bytes(), pk.Bytes(), &EnvelopeOpt{
		AuthTag: authTag,
	}
}

func (o *Optimal) RecoverSecret(unblinded, pks []byte, envU *EnvelopeOpt) (group.Scalar, error) {
	sk, authTag := o.buildKeys(unblinded, pks)

	if !hmac.Equal(authTag, envU.AuthTag) {
		return nil, errors.New("invalid mac")
	}

	return sk, nil
}

func deriveSecretKey(cs ciphersuite.Identifier, prk []byte) group.Scalar {
	dst := "Opaque-KeyGenerationSeed"
	g := cs.Get([]byte(dst)) // expand

	return g.HashToScalar(prk)
}

func (o *Optimal) buildKeys(unblinded, pks []byte) (group.Scalar, []byte) {
	hardened := o.Parameters.Hash(unblinded, nil)
	h := o.Hash.Get()
	prk := h.HKDFExtract(hardened, nil)
	sk := deriveSecretKey(o.Group, prk)
	authKey := h.HKDFExpand(prk, []byte("AuthKey"), 0)
	authTag := h.Hmac(utils.Concatenate(0, pks), authKey)

	return sk, authTag
}
