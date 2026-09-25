package speech

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
)

// Audio summarizes a 16-bit PCM WAV file: enough to decide, before any
// transcription, whether a recording contains an answer at all.
type Audio struct {
	Seconds float64
	RMS     float64 // 0..1, relative to full scale
	Rate    int
}

// AnalyzeWAV reads a WAV file and returns its duration and RMS level. It
// tolerates truncated data chunks (a recorder killed mid-write) by clamping
// the declared size to the file.
func AnalyzeWAV(path string) (Audio, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Audio{}, err
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return Audio{}, errors.New("not a RIFF/WAVE file")
	}
	var rate, channels, bits int
	var pcm []byte
	off := 12
	for off+8 <= len(data) {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		body := off + 8
		end := body + size
		if end > len(data) || size < 0 {
			end = len(data)
		}
		switch id {
		case "fmt ":
			if end-body >= 16 {
				channels = int(binary.LittleEndian.Uint16(data[body+2:]))
				rate = int(binary.LittleEndian.Uint32(data[body+4:]))
				bits = int(binary.LittleEndian.Uint16(data[body+14:]))
			}
		case "data":
			pcm = data[body:end]
		}
		off = end + (size & 1)
		if end == len(data) {
			break
		}
	}
	if rate == 0 || channels == 0 {
		return Audio{}, errors.New("missing fmt chunk")
	}
	if bits != 16 {
		return Audio{}, fmt.Errorf("unsupported sample size %d bits", bits)
	}
	n := len(pcm) / 2
	var sumsq float64
	for i := 0; i < n; i++ {
		s := float64(int16(binary.LittleEndian.Uint16(pcm[2*i:])))
		sumsq += s * s
	}
	a := Audio{Rate: rate, Seconds: float64(n/channels) / float64(rate)}
	if n > 0 {
		a.RMS = math.Sqrt(sumsq/float64(n)) / 32768
	}
	return a, nil
}

// WriteWAV writes 16-bit mono PCM samples; used by tests and the doctor.
func WriteWAV(path string, rate int, samples []int16) error {
	dataSize := len(samples) * 2
	buf := make([]byte, 44+dataSize)
	copy(buf[0:], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(36+dataSize))
	copy(buf[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:], 1) // mono
	binary.LittleEndian.PutUint32(buf[24:], uint32(rate))
	binary.LittleEndian.PutUint32(buf[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(buf[32:], 2)
	binary.LittleEndian.PutUint16(buf[34:], 16)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], uint32(dataSize))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[44+2*i:], uint16(s))
	}
	return os.WriteFile(path, buf, 0o644)
}
