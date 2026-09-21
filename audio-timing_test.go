package main

import (
	"bytes"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
)

func TestAudioPrimingPreservesPresentationTimeInSampleUnits(t *testing.T) {
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(10000000, "audio", "eng")
	track := init.Moov.Traks[0]
	if err := track.SetAACDescriptor(2, 48000); err != nil {
		t.Fatal(err)
	}
	track.Mdia.Mdhd.Timescale = 10000000
	edit := &mp4.ElstBox{Entries: []mp4.ElstEntry{{MediaTime: 426667, SegmentDuration: 1000}, {MediaTime: -1, SegmentDuration: 50}}}
	edts := &mp4.EdtsBox{}
	edts.AddChild(edit)
	edts.Elst = []*mp4.ElstBox{edit}
	track.AddChild(edts)
	fragment, err := mp4.CreateFragment(1, track.Tkhd.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte{1, 2, 3, 4}
	fragment.AddFullSample(mp4.FullSample{Sample: mp4.Sample{Dur: 213333, Size: 4}, DecodeTime: 10000000, Data: payload})
	audio := &mp4.File{Init: init, Segments: []*mp4.MediaSegment{{Fragments: []*mp4.Fragment{fragment}}}}
	if err := audioSampleTimescale(audio); err != nil {
		t.Fatal(err)
	}
	if track.Mdia.Mdhd.Timescale != 48000 || edit.Entries[0].MediaTime != 2048 || edit.Entries[0].SegmentDuration != 1000 || edit.Entries[1].MediaTime != -1 {
		t.Fatalf("priming: scale=%d media=%d duration=%d empty=%d edts=%v", track.Mdia.Mdhd.Timescale, edit.Entries[0].MediaTime, edit.Entries[0].SegmentDuration, edit.Entries[1].MediaTime, track.Edts)
	}
	if fragment.Moof.Traf.Tfdt.BaseMediaDecodeTime() != 48000 || fragment.Moof.Traf.Trun.Samples[0].Dur != 1024 {
		t.Fatal("sample timing changed")
	}
	if !bytes.Equal(fragment.Mdat.Data, payload) {
		t.Fatal("audio payload changed")
	}
	if err := audioSampleTimescale(audio); err != nil {
		t.Fatal(err)
	}
	if edit.Entries[0].MediaTime != 2048 {
		t.Fatal("second normalization changed timing")
	}
}
