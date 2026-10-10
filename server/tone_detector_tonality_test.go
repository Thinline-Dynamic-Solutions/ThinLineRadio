package main

import (
	"math"
	"math/rand"
	"testing"
)

func tonalityOf(d *ToneDetector, gen func(i int) float64) float64 {
	const n, sr = 2048, 16000
	w := make([]float64, n)
	for i := range w {
		w[i] = gen(i) * 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return spectralTonality(d.dft(w, sr), n, sr)
}

func TestSpectralTonality(t *testing.T) {
	d := NewToneDetector()
	const sr = 16000.0

	// A dispatch page tone (pure sinusoid, optionally with a harmonic) must be treated as a tone.
	pure := tonalityOf(d, func(i int) float64 { return 0.8 * math.Sin(2*math.Pi*1185.6*float64(i)/sr) })
	if pure < minTranscriptionToneTonality {
		t.Errorf("pure tone tonality = %.2f, want >= %.2f", pure, minTranscriptionToneTonality)
	}
	withHarmonic := tonalityOf(d, func(i int) float64 {
		x := float64(i) / sr
		return 0.8*math.Sin(2*math.Pi*1150.5*x) + 0.3*math.Sin(2*math.Pi*3451.5*x)
	})
	if withHarmonic < minTranscriptionToneTonality {
		t.Errorf("tone + harmonic tonality = %.2f, want >= %.2f", withHarmonic, minTranscriptionToneTonality)
	}

	// A voiced vowel (steady pitch, many harmonics with a formant-like envelope, plus breath noise)
	// must NOT be treated as a tone, even though its pitch is stable. This was the bug that made
	// real dispatches get skipped as "mostly tones".
	rng := rand.New(rand.NewSource(1))
	vowel := tonalityOf(d, func(i int) float64 {
		x := float64(i) / sr
		var s float64
		for h := 1; h <= 25; h++ {
			amp := 0.5 / math.Sqrt(float64(h))
			s += amp * math.Sin(2*math.Pi*140*float64(h)*x+float64(h))
		}
		return 0.3*s + 0.05*rng.NormFloat64()
	})
	if vowel >= minTranscriptionToneTonality {
		t.Errorf("voiced vowel tonality = %.2f, want < %.2f", vowel, minTranscriptionToneTonality)
	}
}
