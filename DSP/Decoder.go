package DSP

import (
	"bytes"
	"encoding/binary"
	. "github.com/logrusorgru/aurora"
	"github.com/opensatelliteproject/SatHelperApp/Logger"
	"github.com/opensatelliteproject/SatHelperApp/Models"
	"github.com/opensatelliteproject/SatHelperApp/metrics"
	"github.com/opensatelliteproject/libsathelper"
	"github.com/racerxdl/go.fifo"
	"log"
	"time"
)

const maxViterbiErrors = 500

func InitDecoder() {
	if CurrentConfig.Decoder.UseLastFrameData {
		viterbiData = make([]byte, CodedFrameSize+LastFrameDataBits)
		decodedData = make([]byte, FrameSize+LastFrameData)
		lastFrameEnd = make([]byte, LastFrameDataBits)

		viterbi = SatHelper.NewViterbi27(FrameBits + LastFrameDataBits)

		for i := 0; i < LastFrameDataBits; i++ {
			lastFrameEnd[i] = 128
		}
	} else {
		viterbiData = make([]byte, CodedFrameSize)
		decodedData = make([]byte, FrameSize)

		viterbi = SatHelper.NewViterbi27(FrameBits)
	}

	codedData = make([]byte, CodedFrameSize)
	rsCorrectedData = make([]byte, FrameSize)
	rsWorkBuffer = make([]byte, 255)

	reedSolomon = SatHelper.NewReedSolomon()
	correlator = SatHelper.NewCorrelator()
	packetFixer = SatHelper.NewPacketFixer()

	syncWord = make([]byte, 4)

	reedSolomon.SetCopyParityToOutput(true)

	if CurrentConfig.Base.Mode == "lrit" {
		correlator.AddWord(LritUw0)
		correlator.AddWord(LritUw2)
	} else {
		correlator.AddWord(HritUw0)
		correlator.AddWord(HritUw2)
	}

	symbolsFifo = fifo.NewQueue()

	log.Printf(Cyan("Use Last Frame Data: %t").String(), Bold(Green(CurrentConfig.Decoder.UseLastFrameData)))
}

func decoderLoop() {
	var isCorrupted bool
	lastFrameOk := false

	var localStats Models.Statistics
	var averageRSCorrections float32 = 0.0
	var averageVitCorrections float32 = 0.0
	var lostPacketsPerChannel [256]int64
	var lastPacketCount [256]int64
	var receivedPacketsPerChannel [256]int64
	var flywheelCount = 0

	for IsRunning() {
		if symbolsFifo.Len() >= CodedFrameSize {
			decodFifoUsage = uint8(100 * float32(symbolsFifo.Len()) / float32(FifoSize))
			metrics.DemodulatorFifoUsage(float64(demodFifoUsage))
			if localStats.TotalPackets%AverageLastNSamples == 0 {
				averageRSCorrections = 0
				averageVitCorrections = 0
			}
			symbolsFifo.UnsafeLock()
			for i := 0; i < CodedFrameSize; i++ {
				codedData[i] = symbolsFifo.UnsafeNext().(byte)
			}
			symbolsFifo.UnsafeUnlock()

			if flywheelCount == DefaultFlywheelRecheck {
				lastFrameOk = false
				flywheelCount = 0
			}

			// This reduces CPU Usage
			if !lastFrameOk {
				correlator.Correlate(&codedData[0], CodedFrameSize)
			} else {
				// If we got a good lock before, let's just check if the sync is in correct pos.

				correlator.Correlate(&codedData[0], CodedFrameSize/64)
				if correlator.GetHighestCorrelationPosition() != 0 {
					// Oh no, that means something happened :/
					correlator.Correlate(&codedData[0], CodedFrameSize)
					//lastFrameOk = false // GoLintCLI says this useless?
					flywheelCount = 0
				}
			}
			flywheelCount++

			word := correlator.GetCorrelationWordNumber()
			pos := correlator.GetHighestCorrelationPosition()
			corr := correlator.GetHighestCorrelation()
			phaseShift := SatHelper.DEG_0
			if word == 1 {
				phaseShift = SatHelper.DEG_180
			}

			metrics.SyncCorrelation(int(corr))

			if corr < MinCorrelationBits {
				SLog.Error("Correlation didn't match criteria of %d bits. Got %d", Bold(MinCorrelationBits), Bold(corr))
				lastFrameOk = false
				metrics.SignalStatus(false)
				continue
			}

			if pos != 0 {
				// Sync frame
				shiftWithConstantSize(&codedData, int(pos), CodedFrameSize)
				for symbolsFifo.Len() < int(pos) {
					// Wait enough data
					time.Sleep(time.Microsecond)
				}
				offset := CodedFrameSize - pos
				symbolsFifo.UnsafeLock()
				for i := offset; i < CodedFrameSize; i++ {
					codedData[i] = symbolsFifo.UnsafeNext().(byte)
				}
				symbolsFifo.UnsafeUnlock()
			}

			if CurrentConfig.Base.Mode == "lrit" {
				packetFixer.FixPacket(&codedData[0], CodedFrameSize, phaseShift, false)
			}

			if CurrentConfig.Decoder.UseLastFrameData {
				for i := 0; i < LastFrameDataBits; i++ {
					viterbiData[i] = lastFrameEnd[i]
				}
				for i := LastFrameDataBits; i < CodedFrameSize+LastFrameDataBits; i++ {
					viterbiData[i] = codedData[i-LastFrameDataBits]
				}
			} else {
				for i := 0; i < CodedFrameSize; i++ {
					viterbiData[i] = codedData[i]
				}
			}

			viterbi.Decode(&viterbiData[0], &decodedData[0])

			if CurrentConfig.Base.Mode == "hrit" {
				nrzmDecodeSize := FrameSize
				if CurrentConfig.Decoder.UseLastFrameData {
					nrzmDecodeSize += LastFrameData
				}

				SatHelper.DifferentialEncodingNrzmDecode(&decodedData[0], nrzmDecodeSize)
			}

			vitBitErr := viterbi.GetBER()
			vitBitErr -= LastFrameDataBits / 2

			if vitBitErr < 0 { // Dont account for last frame data bit errors
				vitBitErr = 0
			}

			metrics.Viterbi(vitBitErr)

			signalQuality := 100 * ((float32(maxViterbiErrors) - float32(vitBitErr)) / float32(maxViterbiErrors))
			if signalQuality > 100 {
				signalQuality = 100
			} else if signalQuality < 0 {
				signalQuality = 0
			}

			metrics.SignalQuality(int(signalQuality))

			averageVitCorrections += float32(vitBitErr)

			if CurrentConfig.Decoder.UseLastFrameData {
				shiftWithConstantSize(&decodedData, LastFrameData/2, FrameSize+LastFrameData/2)
				for i := 0; i < LastFrameDataBits; i++ {
					lastFrameEnd[i] = viterbiData[CodedFrameSize+i]
				}
			}

			for i := 0; i < SyncWordSize; i++ {
				syncWord[i] = decodedData[i]
				localStats.SyncWord[i] = decodedData[i]
			}

			shiftWithConstantSize(&decodedData, SyncWordSize, FrameSize-SyncWordSize)

			localStats.AverageVitCorrections += uint16(vitBitErr)
			localStats.TotalPackets += 1

			SatHelper.DeRandomizerDeRandomize(&decodedData[0], FrameSize-SyncWordSize)

			derrors := make([]int32, RsBlocks)

			totalBytesFixed := int32(0)

			for i := 0; i < RsBlocks; i++ {
				reedSolomon.Deinterleave(&decodedData[0], &rsWorkBuffer[0], byte(i), RsBlocks)
				derrors[i] = int32(int8(reedSolomon.Decode_ccsds(&rsWorkBuffer[0])))
				reedSolomon.Interleave(&rsWorkBuffer[0], &rsCorrectedData[0], byte(i), RsBlocks)
				if derrors[i] != -1 {
					averageRSCorrections += float32(derrors[i])
				}
				localStats.RsErrors[i] = derrors[i]
				if derrors[i] > -1 {
					totalBytesFixed += derrors[i]
				}
			}

			metrics.ReedSolomon(int(totalBytesFixed))

			if derrors[0] == -1 && derrors[1] == -1 && derrors[2] == -1 && derrors[3] == -1 {
				isCorrupted = true
				lastFrameOk = false
				localStats.DroppedPackets += 1
				metrics.SignalStatus(false)
			} else {
				isCorrupted = false
				lastFrameOk = true
				metrics.SignalStatus(true)
			}

			scid := ((rsCorrectedData[0] & 0x3F) << 2) | (rsCorrectedData[1]&0xC0)>>6
			vcid := rsCorrectedData[1] & 0x3F
			counter := uint(rsCorrectedData[2])
			counter = SatHelper.ToolsSwapEndianess(counter)
			counter &= 0xFFFFFF00
			counter >>= 8

			localStats.StartTime = startTime

			localStats.DecoderFifoUsage = GetDecoderFIFOUsage()
			localStats.DemodulatorFifoUsage = GetDemodFIFOUsage()

			localStats.SCID = scid
			localStats.VCID = vcid

			localStats.PacketNumber = uint64(counter)
			localStats.VitErrors = uint16(vitBitErr)
			localStats.FrameBits = FrameBits
			localStats.SignalQuality = uint8(signalQuality)
			localStats.SyncCorrelation = uint8(corr)

			switch phaseShift {
			case SatHelper.DEG_0:
				localStats.PhaseCorrection = 0
			case SatHelper.DEG_90:
				localStats.PhaseCorrection = 1
			case SatHelper.DEG_180:
				localStats.PhaseCorrection = 2
			case SatHelper.DEG_270:
				localStats.PhaseCorrection = 3
			}

			if !isCorrupted {
				if lastPacketCount[vcid]+1 != int64(counter) && lastPacketCount[vcid] > -1 {
					lostCount := int(int64(counter) - lastPacketCount[vcid] - 1)
					localStats.LostPackets += uint64(lostCount)
					lostPacketsPerChannel[vcid] += int64(lostCount)
				}
				lastPacketCount[vcid] = int64(counter)
				if receivedPacketsPerChannel[vcid] == -1 {
					receivedPacketsPerChannel[vcid] = 1
				} else {
					receivedPacketsPerChannel[vcid] = receivedPacketsPerChannel[vcid] + 1
				}

				if localStats.TotalPackets%AverageLastNSamples == 0 {
					localStats.AverageRSCorrections = uint8(averageRSCorrections / 4)
					localStats.AverageVitCorrections = uint16(averageVitCorrections)
				} else {
					localStats.AverageRSCorrections = uint8(averageRSCorrections / float32(4*(localStats.TotalPackets%AverageLastNSamples)))
					localStats.AverageVitCorrections = uint16(averageVitCorrections / float32(localStats.TotalPackets%AverageLastNSamples))
				}
				localStats.FrameLock = 1
				for i := 0; i < 256; i++ {
					localStats.ReceivedPacketsPerChannel[i] = receivedPacketsPerChannel[i]
					localStats.LostPacketsPerChannel[i] = lostPacketsPerChannel[i]
				}
				if SDemuxer != nil {
					SDemuxer.SendData(rsCorrectedData[:FrameSize-RsParityBlockSize-SyncWordSize])
				}
			} else {
				localStats.FrameLock = 0
			}

			SetStats(localStats)

			if StatisticsServer != nil {
				var statBuff bytes.Buffer
				err := binary.Write(&statBuff, binary.LittleEndian, localStats)
				if err != nil {
					SLog.Error("Error parsing statistics: %s", err)
				}
				StatisticsServer.SendData(statBuff.Bytes())
			}
		} else {
			time.Sleep(5 * time.Microsecond)
		}
	}
}
