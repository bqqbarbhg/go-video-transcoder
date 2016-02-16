package transcode

import (
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
)

// Regex that matches `exiftool` output format
var reRotation = regexp.MustCompile("Rotation\\s*:\\s*(\\d+)")

// Extracts the rotation from the metadata of the video at `videoPath`
func ExtractRotation(videoPath string) (int, error) {

	// Call `exiftool` to read the metadata of the video
	rotationCmd := exec.Command("exiftool", "-Rotation", videoPath)
	rotationOutput, err := rotationCmd.Output()
	if err != nil {
		return 0, err
	}

	// Parse the output for "Rotation: \d+"
	matches := reRotation.FindSubmatch(rotationOutput)
	if len(matches) < 2 {
		return 0, errors.New("Did not find rotation from exiftool output")
	}

	rotation, err := strconv.Atoi(string(matches[1]))
	if err != nil {
		return 0, err
	}

	return rotation, nil
}

// Extracts duration of the video at `videoPath`
func ExtractDuration(videoPath string) (float64, error) {

	// Call `avprobe` to read the metadata of the video
	durationCmd := exec.Command("avprobe", "-v", "quiet", "-show_format_entry", "duration", videoPath)
	durationOutput, err := durationCmd.Output()
	if err != nil {
		return 0.0, err
	}

	// Parse the duration from the outupt
	var duration float64
	num, err := fmt.Sscanf(string(durationOutput), "%f", &duration)
	if err != nil {
		return 0.0, err
	} else if num != 1 {
		return 0.0, fmt.Errorf("Failed to parse duration of %s", videoPath)
	}

	return duration, nil
}

// Represents a quality setting for transcoding
type Quality int

const (
	// Low quality, fast processing
	QualityLow Quality = iota

	// High quality, slow processing
	QualityHigh
)

// For use with `TranscodeMP4`
type Options struct {

	// Rotates the video when transcoding (see `ExtractRotation`)
	CompensateRotation int

	// Quality/performance setting (see `TranscodeQuality`)
	Quality Quality

	// Custom arguments for the transcoder
	ExtraArgs []string
}

// Arguments to normalize a rotation of a video
var rotationAvconvArguments = map[int][]string{
	0:   {},
	90:  {"-vf", "transpose=1"},
	180: {"-vf", "vflip,hflip"},
	270: {"-vf", "transpose=3"},
}

// Arguments for specified quality settings
var qualityAvconvArguments = map[Quality][]string{
	QualityLow:  {"-preset", "ultrafast"},
	QualityHigh: {"-qscale", "1"},
}

func appendOptions(args []string, options *Options) []string {
	if options == nil {
		return args
	}

	// Rotation compensation
	rotationArgs := rotationAvconvArguments[options.CompensateRotation]
	if rotationArgs != nil && len(rotationArgs) > 0 {
		args = append(args, rotationArgs...)
	}

	// Quality settings
	qualityArgs := qualityAvconvArguments[options.Quality]
	if qualityArgs != nil && len(qualityArgs) > 0 {
		args = append(args, qualityArgs...)
	}

	// Custom arguments
	if len(options.ExtraArgs) > 0 {
		args = append(args, options.ExtraArgs...)
	}

	return args
}

// Synchronously transcode a video from `src` to `dst` using `options`
// See `TranscodeOptions`
func TranscodeMP4(src string, dst string, options *Options) error {
	args := []string{
		// Input file
		"-i", src,

		// Overwrite
		"-y",

		// Convert audio: copy
		"-c:a", "copy",

		// Convert video: h264
		"-c:v", "h264",

		// Log level
		"-v", "warning",
	}

	// Options
	args = appendOptions(args, options)

	// Output file
	args = append(args, dst)

	// Call `avconv` to do the transcoding
	transcodeCmd := exec.Command("avconv", args...)
	err := transcodeCmd.Run()

	return err
}

// Synchronously generate a thumbnail from a video `src` to `dst`
func GenerateThumbnail(src string, dst string, time float64, options *Options) error {
	args := []string{
		// Input file
		"-i", src,

		// Overwrite
		"-y",

		// Time
		"-ss", fmt.Sprintf("%.4f", time),

		// Only one frame
		"-frames:v", "1",

		// Log level
		"-v", "warning",
	}

	// Options
	args = appendOptions(args, options)

	// Output file
	args = append(args, dst)

	// Call `avconv` to do the transcoding
	transcodeCmd := exec.Command("avconv", args...)
	err := transcodeCmd.Run()

	return err
}
