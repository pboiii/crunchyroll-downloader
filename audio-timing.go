package main

import (
	"bytes"
	"errors"
	"math"
	"os"

	"github.com/Eyevinn/mp4ff/mp4"
)

// Express AAC edit-list priming in sample units before FFmpeg reads it.
func normalizeAudioTiming(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	audio, err := mp4.DecodeFile(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if err := audioSampleTimescale(audio); err != nil {
		return err
	}
	var normalized bytes.Buffer
	if err := audio.Encode(&normalized); err != nil {
		return err
	}
	return os.WriteFile(filename, normalized.Bytes(), 0600)
}

func audioSampleTimescale(audio *mp4.File) error {
	if audio.Init == nil || len(audio.Init.Moov.Traks) != 1 {
		return errors.New("expected one fragmented audio track")
	}
	track := audio.Init.Moov.Traks[0]
	stsd := track.Mdia.Minf.Stbl.Stsd
	if len(stsd.Children) != 1 {
		return errors.New("expected one audio sample description")
	}
	entry, ok := stsd.Children[0].(*mp4.AudioSampleEntryBox)
	if !ok || entry.SampleRate == 0 || track.Mdia.Mdhd.Timescale == 0 {
		return errors.New("invalid audio timescale or sample rate")
	}
	oldScale := track.Mdia.Mdhd.Timescale
	newScale := uint32(entry.SampleRate)
	if oldScale == newScale {
		return nil
	}
	rescale := func(n int64) int64 { return int64(math.Round(float64(n) * float64(newScale) / float64(oldScale))) }
	track.Mdia.Mdhd.Timescale = newScale
	track.Mdia.Mdhd.Duration = uint64(rescale(int64(track.Mdia.Mdhd.Duration)))
	if track.Edts != nil {
		for _, edit := range track.Edts.Elst {
			rescaleAudioEdit(edit, rescale)
		}
	}
	for _, trex := range audio.Init.Moov.Mvex.Trexs {
		trex.DefaultSampleDuration = uint32(rescale(int64(trex.DefaultSampleDuration)))
	}
	for _, segment := range audio.Segments {
		for _, fragment := range segment.Fragments {
			rescaleAudioFragment(fragment, rescale)
		}
	}
	return nil
}

func rescaleAudioFragment(fragment *mp4.Fragment, rescale func(int64) int64) {
	for _, traf := range fragment.Moof.Trafs {
		traf.Tfdt.SetBaseMediaDecodeTime(uint64(rescale(int64(traf.Tfdt.BaseMediaDecodeTime()))))
		traf.Tfhd.DefaultSampleDuration = uint32(rescale(int64(traf.Tfhd.DefaultSampleDuration)))
		for _, run := range traf.Truns {
			rescaleAudioRun(run, rescale)
		}
	}
}

func rescaleAudioEdit(edit *mp4.ElstBox, rescale func(int64) int64) {
	for i := range edit.Entries {
		if edit.Entries[i].MediaTime < 0 {
			continue
		}
		edit.Entries[i].MediaTime = rescale(edit.Entries[i].MediaTime)
	}
}

func rescaleAudioRun(run *mp4.TrunBox, rescale func(int64) int64) {
	for i := range run.Samples {
		run.Samples[i].Dur = uint32(rescale(int64(run.Samples[i].Dur)))
		run.Samples[i].CompositionTimeOffset = int32(rescale(int64(run.Samples[i].CompositionTimeOffset)))
	}
}
