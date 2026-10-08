package computing

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Transcription through a realtime session.
//
// Swan Inference's /v1/audio/transcriptions sends the uploaded file to a
// provider as JSON. A backend with a plain transcription endpoint could take it
// as-is, but a CLIProxyAPI backend on a ChatGPT subscription has none: its
// upstream offers speech only as realtime sessions, and refuses
// transcription-only ones. A full realtime session does transcribe, though —
// commit the audio with input transcription enabled and the transcript arrives
// as an event, without asking the model for a reply. That is what this does:
// about two seconds and ~140 tokens for an eleven-second clip, against ~400
// when the model is also made to answer.
//
// The realtime API takes 24 kHz mono 16-bit PCM. WAV is decoded here; any
// other container needs ffmpeg on PATH, and without it the request is refused
// with a message saying so rather than sent as noise.

// realtimeSessionModel is the realtime model that hosts the session. The
// transcript itself comes from the transcription model named by the request's
// model (via local_model).
const realtimeSessionModel = "gpt-realtime-2.1"

const (
	realtimeSampleRate   = 24000
	maxTranscriptionFile = 25 << 20 // OpenAI's own upload limit
	transcriptionTimeout = 5 * time.Minute
	appendChunkBytes     = realtimeSampleRate * 2 // one second of audio per event
)

// transcriptionRequest is what the hub sends: the multipart upload re-encoded
// as JSON, the file as base64.
type transcriptionRequest struct {
	Model          string `json:"model"`
	File           []byte `json:"file"`
	Filename       string `json:"filename"`
	Language       string `json:"language"`
	Prompt         string `json:"prompt"`
	ResponseFormat string `json:"response_format"`
}

// transcribe answers one transcription request against a realtime backend.
func (s *InferenceService) transcribe(ctx context.Context, endpoint string, request json.RawMessage, transcriptionModel, apiKey string) (json.RawMessage, error) {
	var req transcriptionRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, &ModelServerError{StatusCode: 400, Message: "transcription request is not valid JSON: " + err.Error()}
	}
	if len(req.File) == 0 {
		return nil, &ModelServerError{StatusCode: 400, Message: "transcription request has no audio file"}
	}
	if len(req.File) > maxTranscriptionFile {
		return nil, &ModelServerError{StatusCode: 413, Message: fmt.Sprintf("audio file is %d bytes; the limit is %d", len(req.File), maxTranscriptionFile)}
	}
	switch req.ResponseFormat {
	case "", "json", "text", "verbose_json":
	default:
		return nil, &ModelServerError{StatusCode: 400, Message: fmt.Sprintf("response_format %q is not supported for this model; use json, text or verbose_json", req.ResponseFormat)}
	}
	if transcriptionModel == "" {
		transcriptionModel = req.Model
	}

	pcm, err := toRealtimePCM(ctx, req.File)
	if err != nil {
		return nil, &ModelServerError{StatusCode: 400, Message: err.Error()}
	}

	ctx, cancel := context.WithTimeout(ctx, transcriptionTimeout)
	defer cancel()
	text, usage, err := realtimeTranscribe(ctx, endpoint, apiKey, transcriptionModel, req.Language, req.Prompt, pcm)
	if err != nil {
		return nil, err
	}

	out := map[string]any{"text": text, "usage": usage}
	if req.ResponseFormat == "verbose_json" {
		out["duration"] = float64(len(pcm)) / 2 / realtimeSampleRate
		if req.Language != "" {
			out["language"] = req.Language
		}
	}
	return json.Marshal(out)
}

// realtimeTranscribe runs one realtime session: configure input transcription,
// append the audio, commit, and wait for the transcript.
func realtimeTranscribe(ctx context.Context, endpoint, apiKey, model, language, prompt string, pcm []byte) (string, map[string]any, error) {
	wsURL, err := realtimeURL(endpoint)
	if err != nil {
		return "", nil, err
	}
	header := http.Header{}
	if apiKey != "" {
		header.Set("Authorization", "Bearer "+apiKey)
	}
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		status := 502
		if resp != nil && resp.StatusCode >= 400 {
			status = resp.StatusCode
		}
		return "", nil, &ModelServerError{StatusCode: status, Message: "realtime session could not be opened: " + err.Error()}
	}
	defer conn.Close()
	// Unblock a read when the request is cancelled or times out.
	go func() { <-ctx.Done(); conn.Close() }()

	tx := map[string]any{"model": model}
	if language != "" {
		tx["language"] = language
	}
	if prompt != "" {
		tx["prompt"] = prompt
	}
	send := func(v any) error { return conn.WriteJSON(v) }
	if err := send(map[string]any{"type": "session.update", "session": map[string]any{
		"type":              "realtime",
		"output_modalities": []string{"text"},
		"audio": map[string]any{"input": map[string]any{
			"format":         map[string]any{"type": "audio/pcm", "rate": realtimeSampleRate},
			"turn_detection": nil,
			"transcription":  tx,
		}},
	}}); err != nil {
		return "", nil, &ModelServerError{StatusCode: 502, Message: "realtime session.update failed: " + err.Error()}
	}
	for i := 0; i < len(pcm); i += appendChunkBytes {
		end := min(i+appendChunkBytes, len(pcm))
		if err := send(map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(pcm[i:end])}); err != nil {
			return "", nil, &ModelServerError{StatusCode: 502, Message: "realtime audio append failed: " + err.Error()}
		}
	}
	if err := send(map[string]any{"type": "input_audio_buffer.commit"}); err != nil {
		return "", nil, &ModelServerError{StatusCode: 502, Message: "realtime audio commit failed: " + err.Error()}
	}

	for {
		var ev struct {
			Type       string          `json:"type"`
			Transcript string          `json:"transcript"`
			Usage      map[string]any  `json:"usage"`
			Error      json.RawMessage `json:"error"`
		}
		if err := conn.ReadJSON(&ev); err != nil {
			if ctx.Err() != nil {
				return "", nil, &ModelServerError{StatusCode: 504, Message: "transcription timed out"}
			}
			return "", nil, &ModelServerError{StatusCode: 502, Message: "realtime session ended before a transcript: " + err.Error()}
		}
		switch {
		case ev.Type == "error", strings.HasSuffix(ev.Type, "input_audio_transcription.failed"):
			return "", nil, &ModelServerError{StatusCode: 502, Message: "transcription failed upstream: " + realtimeErrorMessage(ev.Error)}
		case strings.HasSuffix(ev.Type, "input_audio_transcription.completed"):
			return strings.TrimSpace(ev.Transcript), transcriptionUsage(ev.Usage), nil
		}
	}
}

// transcriptionUsage keeps the upstream's token counts and adds the chat names
// that billing and this node's counters read.
func transcriptionUsage(u map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"input_tokens", "output_tokens", "total_tokens"} {
		if v, ok := u[k]; ok {
			out[k] = v
		}
	}
	if v, ok := out["input_tokens"]; ok {
		out["prompt_tokens"] = v
	}
	if v, ok := out["output_tokens"]; ok {
		out["completion_tokens"] = v
	}
	return out
}

func realtimeErrorMessage(raw json.RawMessage) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return e.Message
	}
	if len(raw) > 0 {
		return string(raw)
	}
	return "no detail"
}

// realtimeURL turns a backend's HTTP base URL into its realtime WebSocket URL.
func realtimeURL(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return "", fmt.Errorf("bad endpoint %q: %w", endpoint, err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	u.Path += "/v1/realtime"
	u.RawQuery = url.Values{"model": {realtimeSessionModel}}.Encode()
	return u.String(), nil
}

// toRealtimePCM converts an uploaded audio file to 24 kHz mono 16-bit PCM.
func toRealtimePCM(ctx context.Context, file []byte) ([]byte, error) {
	if len(file) >= 12 && string(file[0:4]) == "RIFF" && string(file[8:12]) == "WAVE" {
		return decodeWAV(file)
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, errors.New("only WAV audio is accepted by this provider (install ffmpeg for mp3, m4a, ogg, webm and flac)")
	}
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error",
		"-i", "pipe:0", "-f", "s16le", "-ac", "1", "-ar", fmt.Sprint(realtimeSampleRate), "pipe:1")
	cmd.Stdin = bytes.NewReader(file)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("could not decode the audio file: %s", strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// decodeWAV reads a RIFF/WAVE file — integer PCM at 8, 16, 24 or 32 bits, or
// 32-bit float — and returns it as 24 kHz mono 16-bit PCM.
func decodeWAV(file []byte) ([]byte, error) {
	var (
		format, channels, bits uint16
		rate                   uint32
		data                   []byte
		haveFmt                bool
	)
	for pos := 12; pos+8 <= len(file); {
		id := string(file[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(file[pos+4 : pos+8]))
		body := file[pos+8:]
		if size > len(body) {
			size = len(body) // tolerate a truncated or streaming-written last chunk
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("WAV fmt chunk is too short")
			}
			format = binary.LittleEndian.Uint16(body[0:2])
			channels = binary.LittleEndian.Uint16(body[2:4])
			rate = binary.LittleEndian.Uint32(body[4:8])
			bits = binary.LittleEndian.Uint16(body[14:16])
			if format == 0xFFFE && size >= 26 { // WAVE_FORMAT_EXTENSIBLE: real format in the sub-format GUID
				format = binary.LittleEndian.Uint16(body[24:26])
			}
			haveFmt = true
		case "data":
			data = body[:size]
		}
		pos += 8 + size + size%2
	}
	if !haveFmt || data == nil {
		return nil, errors.New("WAV file has no fmt or data chunk")
	}
	if channels == 0 || rate == 0 {
		return nil, errors.New("WAV file declares no channels or sample rate")
	}
	sample, width, err := wavSampleReader(format, bits)
	if err != nil {
		return nil, err
	}
	frame := width * int(channels)
	frames := len(data) / frame
	mono := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < int(channels); c++ {
			sum += sample(data[i*frame+c*width:])
		}
		mono[i] = sum / float64(channels)
	}
	return resampleToPCM16(mono, int(rate), realtimeSampleRate), nil
}

// wavSampleReader returns a decoder for one sample in [-1, 1] and its width.
func wavSampleReader(format, bits uint16) (func([]byte) float64, int, error) {
	switch {
	case format == 1 && bits == 8:
		return func(b []byte) float64 { return (float64(b[0]) - 128) / 128 }, 1, nil
	case format == 1 && bits == 16:
		return func(b []byte) float64 { return float64(int16(binary.LittleEndian.Uint16(b))) / 32768 }, 2, nil
	case format == 1 && bits == 24:
		return func(b []byte) float64 {
			v := int32(b[0]) | int32(b[1])<<8 | int32(int8(b[2]))<<16
			return float64(v) / 8388608
		}, 3, nil
	case format == 1 && bits == 32:
		return func(b []byte) float64 { return float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648 }, 4, nil
	case format == 3 && bits == 32:
		return func(b []byte) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))) }, 4, nil
	}
	return nil, 0, fmt.Errorf("unsupported WAV encoding (format %d, %d-bit); use 16-bit PCM", format, bits)
}

// resampleToPCM16 linearly resamples mono samples to the target rate and
// encodes them as little-endian 16-bit PCM. Linear interpolation is enough
// for speech recognition, which is all this feeds.
func resampleToPCM16(in []float64, from, to int) []byte {
	if len(in) == 0 {
		return nil
	}
	n := int(int64(len(in)) * int64(to) / int64(from))
	out := make([]byte, 2*n)
	step := float64(from) / float64(to)
	for i := 0; i < n; i++ {
		pos := float64(i) * step
		j := int(pos)
		v := in[min(j, len(in)-1)]
		if j+1 < len(in) {
			frac := pos - float64(j)
			v = v*(1-frac) + in[j+1]*frac
		}
		v = math.Max(-1, math.Min(1, v))
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(math.Round(v*32767))))
	}
	return out
}
