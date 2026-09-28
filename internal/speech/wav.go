package speech

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
)

// Audio summarizes a PCM WAV file: enough to decide, before any
// transcription, whether a recording contains an answer at all.
type Audio struct {
	Seconds float64
	RMS     float64 // 0..1, relative to full scale
	Rate    int
	Bits    int // sample size; whisper-cli only reads 16-bit files
}

// WAV format tags.
const (
	wavPCM        = 1
	wavFloat      = 3
	wavExtensible = 0xFFFE
)

// AnalyzeWAV reads a WAV file and returns its duration and RMS level. It
// accepts 8, 16, 24 and 32-bit integer PCM and 32-bit float, plain or
// WAVE_FORMAT_EXTENSIBLE (sox writes 32-bit extensible files from CoreAudio
// unless told otherwise), and tolerates truncated data chunks (a recorder
// killed mid-write) by clamping the declared size to the file.
func AnalyzeWAV(path string) (Audio, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Audio{}, err
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return Audio{}, errors.New("not a RIFF/WAVE file")
	}
	var rate, channels, bits, format int
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
				format = int(binary.LittleEndian.Uint16(data[body:]))
				channels = int(binary.LittleEndian.Uint16(data[body+2:]))
				rate = int(binary.LittleEndian.Uint32(data[body+4:]))
				bits = int(binary.LittleEndian.Uint16(data[body+14:]))
				if format == wavExtensible && end-body >= 26 {
					// The real encoding is the first field of the SubFormat GUID.
					format = int(binary.LittleEndian.Uint16(data[body+24:]))
				}
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
	width := bits / 8
	switch {
	case bits%8 != 0 || width < 1 || width > 4:
		return Audio{}, fmt.Errorf("unsupported sample size %d bits", bits)
	case format == wavFloat && bits != 32:
		return Audio{}, fmt.Errorf("unsupported float sample size %d bits", bits)
	case format != wavPCM && format != wavFloat:
		return Audio{}, fmt.Errorf("unsupported WAV encoding (format tag %d)", format)
	}
	n := len(pcm) / width
	var sumsq float64
	for i := 0; i < n; i++ {
		s := sample(pcm[i*width:], width, format)
		sumsq += s * s
	}
	a := Audio{Rate: rate, Bits: bits, Seconds: float64(n/channels) / float64(rate)}
	if n > 0 {
		a.RMS = math.Sqrt(sumsq / float64(n))
	}
	return a, nil
}

// sample decodes one little-endian sample into the range -1..1.
func sample(b []byte, width, format int) float64 {
	switch {
	case format == wavFloat:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	case width == 1: // 8-bit WAV is unsigned
		return (float64(b[0]) - 128) / 128
	case width == 2:
		return float64(int16(binary.LittleEndian.Uint16(b))) / 32768
	case width == 3:
		v := int32(b[0]) | int32(b[1])<<8 | int32(int8(b[2]))<<16
		return float64(v) / 8388608
	default:
		return float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
	}
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
