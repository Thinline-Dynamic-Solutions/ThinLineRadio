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
			// For over-modulated signals, we add a limiter BEFORE loudnorm to catch extreme peaks
			switch mode {
			case AUDIO_CONVERSION_CONSERVATIVE_NORM:
				// -16 LUFS: Broadcast TV/radio standard (EBU R128)
				// Pre-limit peaks at -1 dB, then normalize with linear mode for better quality
				args = append(args, "-af", "apad=whole_dur=3s,alimiter=limit=0.9:attack=5:release=50,loudnorm=I=-16:TP=-1.5:LRA=11:dual_mono=true:linear=true")
				
			case AUDIO_CONVERSION_STANDARD_NORM:
				// -12 LUFS: Modern streaming standard (YouTube, Spotify)
				// 4 dB louder than conservative, good balance
				args = append(args, "-af", "apad=whole_dur=3s,alimiter=limit=0.9:attack=5:release=50,loudnorm=I=-12:TP=-1.0:LRA=8:dual_mono=true:linear=true")
				
			case AUDIO_CONVERSION_AGGRESSIVE_NORM:
				// -10 LUFS: Dispatcher/public safety optimized
				// 6 dB louder, compressed dynamics for consistent volume
				args = append(args, "-af", "apad=whole_dur=3s,alimiter=limit=0.9:attack=5:release=50,loudnorm=I=-10:TP=-0.5:LRA=6:dual_mono=true:linear=true")
				
			case AUDIO_CONVERSION_MAXIMUM_NORM:
				// -8 LUFS: Maximum loudness
				// 8 dB louder, heavily compressed, minimal dynamics
				args = append(args, "-af", "apad=whole_dur=3s,alimiter=limit=0.9:attack=5:release=50,loudnorm=I=-8:TP=-0.2:LRA=5:dual_mono=true:linear=true")
			}
		} else {
			// FFmpeg < 4.3: Fall back to dynamic audio normalization
			// Not as accurate as loudnorm but better than nothing
			if !ffmpeg.warned {
				fmt.Println("Warning: FFmpeg 4.3+ required for loudnorm filter. Using fallback dynaudnorm filter.")
				fmt.Println("For best results, please upgrade FFmpeg to version 4.3 or later.")
				ffmpeg.warned = true
			}
			// dynaudnorm with aggressive compression for over-modulated signals
			args = append(args, "-af", "apad=whole_dur=3s,alimiter=limit=0.9:attack=5:release=50,dynaudnorm=f=100:g=15:p=0.9:m=10:r=0.5:b=1")
		}
	}

	// Determine codec and bitrate from options (or fall back to config/defaults)
	useOpus := false
	bitrate := uint(24) // Default bitrate in kbps
	
	// Check options first (admin UI settings)
	if options != nil {
		if options.AudioCodec == "opus" {
			useOpus = true
		} else if options.AudioCodec == "aac" {
			useOpus = false
		} else if config != nil && config.UseOpus {
			// Fall back to config if no explicit codec selected
			useOpus = true
		}
		
		// Use custom bitrate if specified
		if options.AudioBitrate > 0 {
			bitrate = options.AudioBitrate
		}
	} else if config != nil && config.UseOpus {
		// Fall back to config if options not available
		useOpus = true
	}
	
	// Apply codec-specific encoding
	if useOpus {
		// Force 16kHz mono for optimal Opus encoding
		args = append(args, "-ar", "16000", "-ac", "1")
		
		// Use Opus codec optimized for voice
		args = append(args, 
			"-c:a", "libopus",
			"-b:a", fmt.Sprintf("%dk", bitrate), // User-configurable bitrate
			"-vbr", "on",               // Variable bitrate
			"-application", "voip",     // Optimize for voice
			"-compression_level", "10", // Max compression
			"-f", "opus",               // Opus/OGG container format
			"-",
		)
	} else {
		// Use AAC/M4A encoding
		if bitrate == 0 {
			bitrate = 32 // Default AAC bitrate
		}
		args = append(args, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", bitrate), "-movflags", "frag_keyframe+empty_moov", "-f", "ipod", "-")
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
