package observers

import (
	"fmt"
	"geo-observers-blockchain/core/chain"
	"geo-observers-blockchain/core/common"
	"geo-observers-blockchain/core/geo"
	"geo-observers-blockchain/core/network/external"
	"geo-observers-blockchain/core/network/messages"
	"geo-observers-blockchain/core/settings"
	"geo-observers-blockchain/core/utils"
	log "github.com/sirupsen/logrus"
	"net"
	"time"
)

// todo: move this to common errors
var (
	ErrEmptyData                     = utils.Error("sender", "invalid data set")
	ErrCycleDataSending              = utils.Error("sender", "attempt to send the data to itself prevented")
	ErrPartialWriteOccurred          = utils.Error("sender", "partial write occurred")
	ErrInvalidSendingAttempt         = utils.Error("sender", "invalid data sending attempt")
	ErrObserverConnectionRefused     = utils.Error("sender", "can't connect to remote observer")
	ErrInvalidObserversConfiguration = utils.Error("sender", "can't connect to remote observer")
	ErrInvalidObserverIndex          = utils.Error("sender", "invalid observer index")
)

// todo: encrypt traffic between observers
type Sender struct {
	Settings         *settings.Settings
	ObserversConfRep *external.Reporter

	OutgoingClaims           chan geo.Claim
	OutgoingTSLs             chan geo.TransactionSignaturesList
	OutgoingProposedBlocks   chan *chain.ProposedBlock
	OutgoingBlocksSignatures chan *messages.SignatureMessage
	connections              *ConnectionsMap
}

func NewSender(conf *settings.Settings, observersConfigurationReporter *external.Reporter) *Sender {
	const kChannelBufferSize = 1

	return &Sender{
		Settings:         conf,
		ObserversConfRep: observersConfigurationReporter,

		OutgoingClaims:           make(chan geo.Claim, kChannelBufferSize),
		OutgoingTSLs:             make(chan geo.TransactionSignaturesList, kChannelBufferSize),
		OutgoingProposedBlocks:   make(chan *chain.ProposedBlock, kChannelBufferSize),
		OutgoingBlocksSignatures: make(chan *messages.SignatureMessage, kChannelBufferSize),

		connections: NewConnectionsMap(time.Minute * 10),
	}
}

func (s *Sender) Run(host string, port uint16, errors chan<- error) {
	// Report Ok
	errors <- nil

	s.waitAndSendInfo(errors)
}

//func (s *Sender) reinitialiseConnections(host string, port uint16, configuration *external.Configuration) {
//	// In case if there are present connections - close all of them.
//	if len(s.connections) > 0 {
//		for _, connection := range s.connections {
//			connection.Close()
//		}
//	}
//
//	// Drop previous connections and writers.
//	s.connections = make(map[*external.Observer]net.Conn)
//	s.writers = make(map[*external.Observer]io.Writer)
//
//	// Create new connection to each one observer listed in configuration.
//	for _, observer := range configuration.Observers {
//		// Prevent attempts to connect to itself.
//		if observer.Host != host || observer.Port != port {
//			s.connectToObserver(observer)
//		}
//	}
//}

func (s *Sender) connectToObserver(o *external.Observer) (connection *ConnectionWrapper, err error) {
	conn, err := net.Dial("tcp", fmt.Sprint(o.Host, ":", o.Port))
	if err != nil {
		// No connection is possible to some of observers.
		// Connection error would be reported, but it would not contain any connection details,
		// so it would be very difficult to know which o can't be reached.
		//
		// To make it possible - some additional log information is printed here.
		additionalInfo := log.Fields{"host": o.Host, "port": o.Port}
		s.log().WithFields(additionalInfo).Errorln("Can't connect to remote observer.")

		return nil, ErrObserverConnectionRefused
	}

	if s.Settings.Debug {
		additionalInfo := log.Fields{"host": o.Host, "port": o.Port}
		s.log().WithFields(additionalInfo).Info("Connected to remote observer.")
	}

	s.connections.Set(o, conn)
	return s.connections.Get(o)
}

func (s *Sender) waitAndSendInfo(errors chan<- error) {

	processSending := func() {
		select {
		case claim := <-s.OutgoingClaims:
			{
				// todo: processSending
				s.log().Info("Claim sent", claim)
			}

		case tsl := <-s.OutgoingTSLs:
			{
				// todo: processSending
				s.log().Info("TSL sent", tsl)
			}

		case proposedBlock := <-s.OutgoingProposedBlocks:
			{
				s.processBlockProposalSending(proposedBlock, errors)
				return
			}

		case blockSignature := <-s.OutgoingBlocksSignatures:
			{
				s.processBlockSignatureSending(blockSignature, errors)
				return
			}

			// ...
			// other cases
		}
	}

	for {
		processSending()
	}
}

// todo: consider sending blocks as a stream: claim after claim with on the fly validation on the receivers part
// (defence from the invalid data sending )
func (s *Sender) processBlockProposalSending(block *chain.ProposedBlock, errors chan<- error) {
	if block == nil {
		errors <- common.ErrNilParameter
		return
	}

	data, err := block.MarshalBinary()
	if err != nil {
		errors <- utils.Wrap(ErrInvalidSendingAttempt, err.Error())
		return
	}

	// Mark data as block proposal and send it.
	data = append(StreamTypeBlockProposal, data...)

	// todo: crate function for observers selection
	s.sendDataToObservers(data, nil, errors)
}

func (s *Sender) processBlockSignatureSending(message *messages.SignatureMessage, errors chan<- error) {
	if message == nil {
		errors <- common.ErrNilParameter
		return
	}

	data, err := message.MarshalBinary()
	if err != nil {
		errors <- utils.Wrap(ErrInvalidSendingAttempt, err.Error())
		return
	}

	// todo: append block hash to have possibility to collect signatures
	//  for various proposed blocks on the receiver's side.

	// Mark data as block proposal and send it.
	data = append(StreamTypeBlockSignature, data...)
	s.sendDataToObservers(data, []uint16{message.AddresseeObserverIndex}, errors)
}

// Note: several errors might occur during data sending.
// There are cases when sending should not be cancelled due to an error,
// but the error itself must be reported.
// For this cases, errors channel is propagated to the method.
func (s *Sender) sendDataToObservers(data []byte, observersIndexes []uint16, errors chan<- error) {
	if len(data) == 0 {
		errors <- ErrEmptyData
		return
	}

	observersConf, err := s.ObserversConfRep.GetCurrentConfiguration()
	if err != nil {
		errors <- ErrInvalidObserversConfiguration
		return
	}

	if observersIndexes == nil {
		// Bytes must be sent to all observers.
		for _, observer := range observersConf.Observers {

			if !s.Settings.Debug {
				// If not debug - prevent sending the data to itself.
				// In debug mode it might be useful to send blocks to itself,
				// to test whole network cycle in one executable process.
				if observer.Host == s.Settings.Observers.Network.Host {
					if observer.Port == s.Settings.Observers.Network.Port {
						continue
					}
				}
			}

			err := s.sendDataToObserver(observer, data)
			if err != nil {
				errors <- err
				continue
			}
		}

	} else {
		for _, index := range observersIndexes {
			if len(observersConf.Observers) < int(index+1) {
				errors <- ErrInvalidObserverIndex
				continue
			}

			observer := observersConf.Observers[index]
			err := s.sendDataToObserver(observer, data)
			if err != nil {
				errors <- err
				continue
			}
		}
	}
}

func (s *Sender) sendDataToObserver(observer *external.Observer, data []byte) (err error) {
	if len(data) == 0 {
		return ErrEmptyData
	}

	if !s.Settings.Debug {
		// If not debug - prevent sending the data to itself.
		// In debug mode it might be useful to send blocks to itself,
		// to test whole network cycle in one executable process.
		if observer.Host == s.Settings.Observers.Network.Host {
			if observer.Port == s.Settings.Observers.Network.Port {
				return ErrCycleDataSending
			}
		}
	}

	conn, err := s.connections.Get(observer)
	if err != nil {

		switch err {
		case ErrNoObserver:
			{
				// In case if there is no connection to observer - it should be created.
				conn, err = s.connectToObserver(observer)
				if err != nil {
					return
				}
			}

		default:
			return
		}
	}

	// Writing data total size.
	dataSize := len(data)
	dataSizeMarshaled := utils.MarshalUint32(uint32(dataSize))
	bytesWritten, err := conn.Writer.Write(dataSizeMarshaled)
	if err != nil {
		return
	}
	if bytesWritten != len(dataSizeMarshaled) {
		err = ErrPartialWriteOccurred
		return
	}

	// todo: split the data to chunks of 2-4Kb in size
	// todo: send them sequentially.

	// Writing data.
	//conn.Connection.(net.TCPConn).SetNoDelay(true)
	bytesWritten, err = conn.Writer.Write(data)
	if err != nil {
		return
	}

	// todo: resend the rest (enhancement).
	if bytesWritten != len(data) {
		err = ErrPartialWriteOccurred
		return
	}

	err = conn.Writer.Flush()
	if err != nil {
		return
	}

	//conn.Connection

	s.log().Info(len(data), "sent")
	return nil
}

func (s *Sender) log() *log.Entry {
	return log.WithFields(log.Fields{"subsystem": "ObserversSender"})
}
