// Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
)

type FFMpeg struct {
	available bool
	version43 bool
	warned    bool
}

func NewFFMpeg() *FFMpeg {
	ffmpeg := &FFMpeg{}

	stdout := bytes.NewBuffer([]byte(nil))

	cmd := exec.Command("ffmpeg", "-version")
	cmd.Stdout = stdout

	if err := cmd.Run(); err == nil {
		ffmpeg.available = true

		if l, err := stdout.ReadString('\n'); err == nil {
			// Updated regex to support multi-digit version numbers (e.g. FFmpeg 8.0, 8.0.1, 10.2.1, etc.)
			// Patch version is optional to handle both "8.0" and "8.0.1" formats
			s := regexp.MustCompile(`.*ffmpeg version .{0,1}([0-9]+)\.([0-9]+)(?:\.[0-9]+)?.*`).ReplaceAllString(strings.TrimSuffix(l, "\n"), "$1.$2")
			v := strings.Split(s, ".")
			if len(v) > 1 {
				if major, err := strconv.Atoi(v[0]); err == nil {
					if minor, err := strconv.Atoi(v[1]); err == nil {
						if major > 4 || (major == 4 && minor >= 3) {
							ffmpeg.version43 = true
						}
					}
				}
			}
		}
	}

	return ffmpeg
}

func (ffmpeg *FFMpeg) Convert(call *Call, systems *Systems, tags *Tags, mode uint, config *Config, options *Options) error {
	var (
		args = []string{"-i", "-"}
		err  error
	)

	if mode == AUDIO_CONVERSION_DISABLED {
		return nil
	}

	if !ffmpeg.available {
		if !ffmpeg.warned {
			ffmpeg.warned = true

			return errors.New("ffmpeg is not available, no audio conversion will be performed")
		}
		return nil
	}

	if tag, ok := tags.GetTagById(call.Talkgroup.TagId); ok {
		args = append(args,
			"-metadata", fmt.Sprintf("album=%v", call.Talkgroup.Label),
			"-metadata", fmt.Sprintf("artist=%v", call.System.Label),
			"-metadata", fmt.Sprintf("date=%v", call.Timestamp),
			"-metadata", fmt.Sprintf("genre=%v", tag),
			"-metadata", fmt.Sprintf("title=%v", call.Talkgroup.Name),
		)
	}

	// Apply audio normalization if requested
	if mode >= AUDIO_CONVERSION_CONSERVATIVE_NORM && mode <= AUDIO_CONVERSION_MAXIMUM_NORM {
		if ffmpeg.version43 {
			// FFmpeg 4.3+ with loudnorm filter
			// Conservative filtering: gentle highpass/lowpass to remove extreme frequencies only
			switch mode {
			case AUDIO_CONVERSION_CONSERVATIVE_NORM:
				// -16 LUFS: Broadcast standard with minimal filtering (80 Hz - 8000 Hz)
				args = append(args, "-af", "highpass=f=80:p=1,lowpass=f=8000:p=1,loudnorm=I=-16:TP=-2.0:LRA=11")

			case AUDIO_CONVERSION_STANDARD_NORM:
				// -12 LUFS: Recommended with gentle filtering (100 Hz - 7000 Hz)
				args = append(args, "-af", "highpass=f=100:p=1,lowpass=f=7000:p=1,loudnorm=I=-12:TP=-1.5:LRA=10")

			case AUDIO_CONVERSION_AGGRESSIVE_NORM:
				// -10 LUFS: Dispatcher optimized with moderate filtering (120 Hz - 6000 Hz)
				args = append(args, "-af", "highpass=f=120:p=1,lowpass=f=6000:p=1,loudnorm=I=-10:TP=-1.5:LRA=9")

			case AUDIO_CONVERSION_MAXIMUM_NORM:
				// -8 LUFS: Very loud with tighter filtering (150 Hz - 5000 Hz)
				args = append(args, "-af", "highpass=f=150:p=1,lowpass=f=5000:p=1,loudnorm=I=-8:TP=-1.0:LRA=8")
			}
		} else {
			// FFmpeg < 4.3: Fall back to dynamic audio normalization
			if !ffmpeg.warned {
				fmt.Println("Warning: FFmpeg 4.3+ required for loudnorm filter. Using fallback dynaudnorm filter.")
				fmt.Println("For best results, please upgrade FFmpeg to version 4.3 or later.")
				ffmpeg.warned = true
			}
			// dynaudnorm fallback with conservative filtering
			args = append(args, "-af", "highpass=f=100:p=1,lowpass=f=7000:p=1,dynaudnorm=f=75:g=9:p=0.95:m=15:r=0.5:b=1")
		}
	}

	// Determine codec and encoding parameters from admin options
	useOpus := false
	if options != nil && options.AudioCodec == "opus" {
		useOpus = true
	}

	// Get bitrate from admin options with codec-specific limits
	bitrate := defaults.options.audioBitrate
	if options != nil && options.AudioBitrate > 0 {
		bitrate = options.AudioBitrate
	}

	// Enforce minimum and codec-specific maximums
	if bitrate < 16 {
		bitrate = 16
	}
	if useOpus && bitrate > 256 {
		bitrate = 256 // FFmpeg libopus max is 256 kbps
	} else if !useOpus && bitrate > 320 {
		bitrate = 320 // AAC max is 320 kbps
	}

	if useOpus {
		// Encode as Opus (max 256 kbps) - Stereo 48 kHz (Opus doesn't support 44.1 kHz)
		args = append(args, "-ac", "2", "-ar", "48000", "-c:a", "libopus", "-b:a", fmt.Sprintf("%dk", bitrate), "-vbr", "on", "-compression_level", "10", "-application", "voip", "-f", "opus", "-")
	} else {
		// Encode as AAC/M4A (max 320 kbps) - Stereo 44.1 kHz
		args = append(args, "-ac", "2", "-ar", "44100", "-c:a", "aac", "-profile:a", "aac_low", "-b:a", fmt.Sprintf("%dk", bitrate), "-movflags", "frag_keyframe+empty_moov", "-f", "ipod", "-")
	}

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdin = bytes.NewReader(call.Audio)

	stdout := bytes.NewBuffer([]byte(nil))
	cmd.Stdout = stdout

	stderr := bytes.NewBuffer([]byte(nil))
	cmd.Stderr = stderr

	if err = cmd.Run(); err == nil {
		call.Audio = stdout.Bytes()
		if useOpus {
			call.AudioFilename = fmt.Sprintf("%v.opus", strings.TrimSuffix(call.AudioFilename, path.Ext((call.AudioFilename))))
			call.AudioMime = "audio/opus"
		} else {
			call.AudioFilename = fmt.Sprintf("%v.m4a", strings.TrimSuffix(call.AudioFilename, path.Ext((call.AudioFilename))))
			call.AudioMime = "audio/mp4"
		}
	} else {
		fmt.Println(stderr.String())
	}

	return nil
}
