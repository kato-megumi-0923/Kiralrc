package app

import (
	"errors"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"github.com/egoist/mygo/audio"
	_ "github.com/egoist/mygo/audio/mp3"
	"github.com/mewkiz/flac"
)

func init() {
	audio.SetLatency(audio.LatencyInteractive)
	audio.RegisterFormat("flac", "fLaC", openFLACDecoder)
}

// flacDecoder adapts github.com/mewkiz/flac to mygo's audio.Decoder (same engine Cloudy uses).
type flacDecoder struct {
	st   *flac.Stream
	rate int
	ch   int
	bps  int
	len  int64
	pend []float32
}

func openFLACDecoder(r io.ReadSeeker) (audio.Decoder, error) {
	st, err := flac.NewSeek(r)
	if err != nil {
		// Fall back to sequential stream if seek table isn't usable
		_, _ = r.Seek(0, io.SeekStart)
		st, err = flac.New(r)
		if err != nil {
			return nil, err
		}
	}
	if st.Info == nil || st.Info.SampleRate == 0 || st.Info.NChannels == 0 {
		return nil, errors.New("flac: missing stream info")
	}
	n := int64(st.Info.NSamples)
	if n == 0 {
		n = -1
	}
	return &flacDecoder{
		st:   st,
		rate: int(st.Info.SampleRate),
		ch:   2,
		bps:  int(st.Info.BitsPerSample),
		len:  n,
	}, nil
}

func (d *flacDecoder) SampleRate() int { return d.rate }
func (d *flacDecoder) Channels() int   { return d.ch }
func (d *flacDecoder) Len() int64      { return d.len }

func (d *flacDecoder) SetPosition(frame int64) error {
	if frame < 0 {
		frame = 0
	}
	d.pend = nil
	_, err := d.st.Seek(uint64(frame))
	return err
}

func (d *flacDecoder) Read(dst []float32) (int, error) {
	written := 0
	for written+1 < len(dst) {
		if len(d.pend) > 0 {
			n := copy(dst[written:], d.pend)
			// Keep frame alignment (2 samples per stereo frame)
			n &^= 1
			written += n
			d.pend = d.pend[n:]
			if len(d.pend) == 0 {
				d.pend = nil
			}
			continue
		}
		f, err := d.st.ParseNext()
		if err != nil {
			if written > 0 {
				return written, nil
			}
			return 0, err
		}
		nFrames := int(f.BlockSize)
		if nFrames == 0 || len(f.Subframes) == 0 {
			continue
		}
		bps := int(f.BitsPerSample)
		if bps == 0 {
			bps = d.bps
		}
		scale := float32(1.0 / math.Ldexp(1, bps-1))
		left := f.Subframes[0].Samples
		right := left
		if len(f.Subframes) >= 2 {
			right = f.Subframes[1].Samples
		}
		buf := make([]float32, nFrames*2)
		for i := 0; i < nFrames; i++ {
			buf[i*2] = float32(left[i]) * scale
			buf[i*2+1] = float32(right[i]) * scale
		}
		d.pend = buf
	}
	return written, nil
}

// audioPlayer wraps mygo's native audio.Player (Cloudy's exact audio output backend).
type audioPlayer struct {
	mu     sync.Mutex
	file   *os.File
	pl     *audio.Player
	loaded string
	dur    float64
	speed  float64
	vol    float64
	volSet bool
}

func (p *audioPlayer) Load(path string) (float64, error) {
	if path == "" {
		return 0, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loaded == path && p.pl != nil {
		return p.dur, nil
	}
	if p.pl != nil {
		p.pl.Close()
		p.pl = nil
	}
	if p.file != nil {
		_ = p.file.Close()
		p.file = nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	pl, err := audio.OpenStream(f)
	if err != nil {
		_ = f.Close()
		return 0, err
	}
	v := 0.50
	if p.volSet {
		v = p.vol
	}
	pl.SetVolume(v)
	if p.speed > 0 {
		pl.SetSpeed(p.speed)
	}
	p.file = f
	p.pl = pl
	p.loaded = path
	p.dur = pl.Duration().Seconds()
	return p.dur, nil
}

func (p *audioPlayer) Play() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pl == nil {
		return nil
	}
	p.pl.Play()
	return nil
}

func (p *audioPlayer) Pause() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pl == nil {
		return nil
	}
	p.pl.Pause()
	return nil
}

func (p *audioPlayer) Seek(sec float64) error {
	if sec < 0 {
		sec = 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pl == nil {
		return nil
	}
	p.pl.Seek(time.Duration(sec * float64(time.Second)))
	return nil
}

func (p *audioPlayer) SetSpeed(ratio float64) error {
	if ratio <= 0 {
		ratio = 1.0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.speed = ratio
	if p.pl != nil {
		p.pl.SetSpeed(ratio)
	}
	return nil
}

func (p *audioPlayer) SetVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.vol = v
	p.volSet = true
	if p.pl != nil {
		p.pl.SetVolume(v)
	}
}

func (p *audioPlayer) Position() (float64, float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pl == nil {
		return 0, p.dur
	}
	dur := p.pl.Duration().Seconds()
	if dur > 0 {
		p.dur = dur
	}
	return p.pl.Position().Seconds(), p.dur
}

// extractWaveformPeaks decodes [tStart, tEnd] from a local audio file (WAV/MP3/FLAC)
// and returns nBars normalized peak amplitudes in [0.06, 1.0].
func extractWaveformPeaks(path string, tStart, tEnd float64, nBars int) []float32 {
	if path == "" || nBars <= 0 || tEnd <= tStart {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	dec, _, err := audio.NewDecoder(f)
	if err != nil {
		return nil
	}
	rate := dec.SampleRate()
	ch := dec.Channels()
	if rate <= 0 || ch <= 0 {
		return nil
	}
	startFrame := int64(max(0, tStart) * float64(rate))
	totalFrames := int64((tEnd - tStart) * float64(rate))
	if totalFrames <= 0 {
		return nil
	}
	if err := dec.SetPosition(startFrame); err != nil {
		return nil
	}
	peaks := make([]float32, nBars)
	framesPerBar := max(int64(1), totalFrames/int64(nBars))
	buf := make([]float32, 4096*ch)
	var frameIdx int64
	var maxPeak float32
	for frameIdx < totalFrames {
		want := min(int64(4096), totalFrames-frameIdx)
		n, err := dec.Read(buf[:want*int64(ch)])
		if n > 0 {
			gotFrames := int64(n / ch)
			for i := int64(0); i < gotFrames; i++ {
				bar := int((frameIdx + i) / framesPerBar)
				if bar >= nBars {
					bar = nBars - 1
				}
				v := float32(math.Abs(float64(buf[i*int64(ch)])))
				if ch >= 2 {
					v2 := float32(math.Abs(float64(buf[i*int64(ch)+1])))
					if v2 > v {
						v = v2
					}
				}
				if v > peaks[bar] {
					peaks[bar] = v
				}
				if v > maxPeak {
					maxPeak = v
				}
			}
			frameIdx += gotFrames
		}
		if err != nil {
			break
		}
	}
	if maxPeak < 0.001 {
		return nil
	}
	for i := range peaks {
		v := peaks[i] / maxPeak
		if v < 0.06 {
			v = 0.06
		}
		peaks[i] = v
	}
	return peaks
}
