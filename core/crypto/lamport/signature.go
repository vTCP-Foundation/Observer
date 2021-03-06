package lamport

import (
	"geo-observers-blockchain/core/common"
	"geo-observers-blockchain/core/common/errors"
	"geo-observers-blockchain/core/settings"
	"geo-observers-blockchain/core/utils"
)

const (
	SignatureBytesSize = 1024 * 8
)

type Signature struct {
	Bytes [SignatureBytesSize]byte
}

func (s *Signature) MarshalBinary() (data []byte, err error) {
	return s.Bytes[:SignatureBytesSize], nil
}

func (s *Signature) UnmarshalBinary(data []byte) error {
	if len(data) < SignatureBytesSize {
		return errors.InvalidDataFormat
	}

	if copy(s.Bytes[:], data[:SignatureBytesSize]) == SignatureBytesSize {
		return nil

	} else {
		return errors.InvalidCopyOperation

	}
}

// --------------------------------------------------------------------------------------------------------------------

var (
	SignaturesMaxCount = settings.GEOTransactionMaxParticipantsCount
)

type Signatures struct {
	At []*Signature
}

func (s *Signatures) Add(sig *Signature) error {
	if sig == nil {
		return errors.NilParameter
	}

	if s.Count() < uint16(SignaturesMaxCount) {
		s.At = append(s.At, sig)
		return nil
	}

	return errors.MaxCountReached
}

func (s *Signatures) Count() uint16 {
	return uint16(len(s.At))
}

func (s *Signatures) MarshalBinary() (data []byte, err error) {
	dataSize :=
		(SignatureBytesSize * s.Count()) + // signatures
			2 // size of uint16

	data = make([]byte, 0, dataSize)
	data = append(data, utils.MarshalUint16(s.Count())...)
	for _, signature := range s.At {
		signatureData, err := signature.MarshalBinary()
		if err != nil {
			return nil, err
		}

		data = append(data, signatureData...)
	}

	return
}

func (s *Signatures) UnmarshalBinary(data []byte) (err error) {
	count, err := utils.UnmarshalUint16(data[:common.Uint16ByteSize])
	if err != nil {
		return
	}

	s.At = make([]*Signature, count, count)
	if count == 0 {
		return
	}

	var (
		offset uint32 = common.Uint16ByteSize
		i      uint16
	)

	for i = 0; i < count; i++ {
		sig := &Signature{}
		err = sig.UnmarshalBinary(data[offset : offset+SignatureBytesSize])
		if err != nil {
			return err
		}

		offset += SignatureBytesSize
		s.At[i] = sig
	}

	return
}
