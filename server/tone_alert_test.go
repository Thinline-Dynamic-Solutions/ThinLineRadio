package main

import (
	"math"
	"math/rand"
	"testing"
)

const alertTestRate = 16000

func alertTestTone(freq, seconds, amp float64) []float64 {
	n := int(seconds * alertTestRate)
	out := make([]float64, n)
	for i := range out {
		out[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/alertTestRate)
	}
	return out
}

func alertTestQuiet(seconds float64, rng *rand.Rand) []float64 {
	out := make([]float64, int(seconds*alertTestRate))
	for i := range out {
		out[i] = 0.002 * rng.NormFloat64()
	}
	return out
}

// alertTestSpeech is a buzzy harmonic signal with a wandering pitch, like voice.
func alertTestSpeech(seconds float64, rng *rand.Rand) []float64 {
	out := make([]float64, int(seconds*alertTestRate))
	phase := 0.0
	for i := range out {
		t := float64(i) / alertTestRate
		f0 := 150 + 60*math.Sin(2*math.Pi*1.7*t) + 20*math.Sin(2*math.Pi*5.3*t)
		phase += 2 * math.Pi * f0 / alertTestRate
		v := 0.0
		for h := 1; h <= 20; h++ {
			v += math.Sin(float64(h)*phase) / float64(h)
		}
		out[i] = 0.2*v + 0.02*rng.NormFloat64()
	}
	return out
}

func alertTestDetect(samples []float64, sets ...ToneSet) []Tone {
	return NewToneDetector().detectAlertTones(samples, alertTestRate, sets)
}

func alertSet(id string, spec AlertToneSpec) ToneSet {
	return ToneSet{Id: id, Label: id, Tolerance: 10, Patterns: []TonePattern{{AlertTone: &spec}}}
}

func TestAlertToneBeeps(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	audio := alertTestQuiet(0.3, rng)
	for i := 0; i < 3; i++ {
		audio = append(audio, alertTestTone(1000, 0.15, 0.5)...)
		audio = append(audio, alertTestQuiet(0.15, rng)...)
	}
	audio = append(audio, alertTestSpeech(2, rng)...)

	three := alertSet("three", AlertToneSpec{Kind: "beeps", Frequency: 1000, MinCount: 3})
	four := alertSet("four", AlertToneSpec{Kind: "beeps", Frequency: 1000, MinCount: 4})
	other := alertSet("other", AlertToneSpec{Kind: "beeps", Frequency: 1400})
	tones := alertTestDetect(audio, three, four, other)
	if len(tones) != 1 || tones[0].Pattern != three.Patterns[0].AlertTone.signature() {
		t.Fatalf("tones = %+v, want only the three-beep pattern", tones)
	}
	if tones[0].StartTime < 0.25 || tones[0].EndTime > 1.3 {
		t.Fatalf("span %.2f-%.2f, want about 0.3-1.1", tones[0].StartTime, tones[0].EndTime)
	}

	matched := NewToneDetector().MatchToneSets(&ToneSequence{HasTones: true, Tones: tones}, []ToneSet{three, four, other})
	if len(matched) != 1 || matched[0].Id != "three" {
		t.Fatalf("matched %v, want three", matched)
	}
}

func TestAlertToneSingleBeep(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	audio := append(alertTestTone(1200, 0.2, 0.4), alertTestSpeech(2, rng)...)
	if tones := alertTestDetect(audio, alertSet("beep", AlertToneSpec{Kind: "beeps", Frequency: 1200})); len(tones) != 1 {
		t.Fatalf("single beep: got %d tones", len(tones))
	}
}

func TestAlertToneWarble(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	audio := alertTestQuiet(0.2, rng)
	for i := 0; i < 8; i++ {
		f := 700.0
		if i%2 == 1 {
			f = 1000
		}
		audio = append(audio, alertTestTone(f, 0.25, 0.5)...)
	}
	audio = append(audio, alertTestSpeech(1.5, rng)...)

	warble := alertSet("warble", AlertToneSpec{Kind: "warble", Frequency: 700, Frequency2: 1000})
	wrong := alertSet("wrong", AlertToneSpec{Kind: "warble", Frequency: 700, Frequency2: 1300})
	tones := alertTestDetect(audio, warble, wrong)
	if len(tones) != 1 || tones[0].Pattern != warble.Patterns[0].AlertTone.signature() {
		t.Fatalf("tones = %+v, want only the 700/1000 warble", tones)
	}
}

func TestAlertToneIgnoresSteadyToneSpeechAndNoise(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	sets := []ToneSet{
		alertSet("beep", AlertToneSpec{Kind: "beeps", Frequency: 1000}),
		alertSet("warble", AlertToneSpec{Kind: "warble", Frequency: 750, Frequency2: 1000}),
	}
	noise := make([]float64, 3*alertTestRate)
	for i := range noise {
		noise[i] = 0.3 * rng.NormFloat64()
	}
	cases := map[string][]float64{
		"steady tone": alertTestTone(1000, 3, 0.5),
		"speech":      alertTestSpeech(6, rng),
		"noise":       noise,
	}
	for name, audio := range cases {
		if tones := alertTestDetect(audio, sets...); len(tones) != 0 {
			t.Errorf("%s: detected %+v", name, tones)
		}
	}
}
