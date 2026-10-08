package computing

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestResolveOperation(t *testing.T) {
	cases := []struct {
		name, declared, category, request, want string
	}{
		{"hub says images", OpImagesGenerations, "", `{"messages":[]}`, OpImagesGenerations},
		{"unknown declaration ignored", "audio/speech", "", `{"messages":[]}`, OpChatCompletions},
		{"image category", "", "image", `{"prompt":"x"}`, OpImagesGenerations},
		{"transcription category", "", "transcription", `{}`, OpTranscriptions},
		{"embedding category", "", "embedding", `{"input":"x"}`, OpEmbeddings},
		{"chat category keeps chat", "", "text-generation", `{"messages":[]}`, OpChatCompletions},
		{"shape: prompt without messages", "", "", `{"model":"m","prompt":"a cat"}`, OpImagesGenerations},
		{"shape: file", "", "", `{"model":"m","file":"AAAA"}`, OpTranscriptions},
		{"shape: input", "", "", `{"model":"m","input":"x"}`, OpEmbeddings},
		{"shape: messages", "", "", `{"model":"m","messages":[]}`, OpChatCompletions},
		{"unparseable defaults to chat", "", "", `not json`, OpChatCompletions},
	}
	for _, c := range cases {
		if got := resolveOperation(c.declared, c.category, json.RawMessage(c.request)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWithChatUsageNames(t *testing.T) {
	in := `{"data":[{"b64_json":"x"}],"usage":{"input_tokens":12,"output_tokens":493,"total_tokens":505}}`
	in2, out2 := extractTokenCounts(withChatUsageNames(json.RawMessage(in)))
	if in2 != 12 || out2 != 493 {
		t.Errorf("counts after renaming = %d/%d, want 12/493", in2, out2)
	}
	chat := `{"usage":{"prompt_tokens":1,"completion_tokens":2}}`
	if got := string(withChatUsageNames(json.RawMessage(chat))); got != chat {
		t.Errorf("a chat usage block was rewritten: %s", got)
	}
}

// wavBytes builds a 16-bit PCM WAV with the given channels and rate.
func wavBytes(channels, rate int, samples []int16) []byte {
	var data bytes.Buffer
	for _, s := range samples {
		_ = binary.Write(&data, binary.LittleEndian, s)
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+data.Len()))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate*channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data.Len()))
	b.Write(data.Bytes())
	return b.Bytes()
}

func TestDecodeWAVDownmixesAndResamples(t *testing.T) {
	// One second of 48 kHz stereo: left +8000, right -4000 → mono +2000.
	samples := make([]int16, 0, 2*48000)
	for i := 0; i < 48000; i++ {
		samples = append(samples, 8000, -4000)
	}
	pcm, err := decodeWAV(wavBytes(2, 48000, samples))
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 2*realtimeSampleRate {
		t.Fatalf("len = %d bytes, want %d (one second at 24 kHz)", len(pcm), 2*realtimeSampleRate)
	}
	if v := int16(binary.LittleEndian.Uint16(pcm[1000:])); v < 1990 || v > 2010 {
		t.Errorf("sample = %d, want ~2000 (mean of the channels)", v)
	}
}

func TestToRealtimePCMRejectsNonWAVWithoutFFmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := toRealtimePCM(context.Background(), []byte("ID3\x04not a wav"))
	if err == nil || !strings.Contains(err.Error(), "only WAV") {
		t.Errorf("err = %v, want the WAV-only explanation", err)
	}
}

// An image request is sent to /v1/images/generations under the local model
// name, and its usage comes back under the names billing reads.
func TestHandleInferenceRoutesImages(t *testing.T) {
	var path, model string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		model, _ = req["model"].(string)
		fmt.Fprint(w, `{"created":1,"data":[{"b64_json":"aGk="}],"usage":{"input_tokens":12,"output_tokens":493}}`)
	}))
	defer backend.Close()
	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["openai/gpt-image-2"] = ModelMapping{Endpoint: backend.URL, LocalModel: "gpt-image-2", Category: "image"}

	resp, err := s.handleInference(context.Background(), InferencePayload{
		ModelID: "openai/gpt-image-2", Operation: OpImagesGenerations,
		Request: json.RawMessage(`{"model":"openai/gpt-image-2","prompt":"a circle"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/images/generations" || model != "gpt-image-2" {
		t.Errorf("backend saw %s with model %q", path, model)
	}
	if in, out := extractTokenCounts(resp.Response); in != 12 || out != 493 {
		t.Errorf("usage = %d/%d, want 12/493", in, out)
	}
}

// The transcription bridge configures input transcription, streams the audio,
// commits it, and returns the transcript the realtime session reports —
// without asking the model for a reply.
func TestHandleInferenceTranscribesThroughRealtime(t *testing.T) {
	var events []string
	var sessionModel, txModel string
	var audioBytes int
	upgrader := websocket.Upgrader{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/realtime" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "unexpected", 400)
			return
		}
		sessionModel = r.URL.Query().Get("model")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var ev map[string]any
			if err := conn.ReadJSON(&ev); err != nil {
				return
			}
			typ, _ := ev["type"].(string)
			events = append(events, typ)
			switch typ {
			case "session.update":
				audio := ev["session"].(map[string]any)["audio"].(map[string]any)["input"].(map[string]any)
				txModel, _ = audio["transcription"].(map[string]any)["model"].(string)
			case "input_audio_buffer.append":
				b, _ := base64.StdEncoding.DecodeString(ev["audio"].(string))
				audioBytes += len(b)
			case "input_audio_buffer.commit":
				_ = conn.WriteJSON(map[string]any{
					"type":       "conversation.item.input_audio_transcription.completed",
					"transcript": " ask not what your country can do for you ",
					"usage":      map[string]any{"input_tokens": 110, "output_tokens": 28, "total_tokens": 138},
				})
			}
		}
	}))
	defer backend.Close()

	s := NewInferenceService("test-node", t.TempDir())
	s.modelMappings["openai/gpt-4o-transcribe"] = ModelMapping{Endpoint: backend.URL, LocalModel: "gpt-4o-transcribe", Category: "transcription", APIKey: "k"}
	half := make([]int16, 12000) // half a second at 24 kHz mono
	req, _ := json.Marshal(transcriptionRequest{Model: "openai/gpt-4o-transcribe", File: wavBytes(1, 24000, half), Filename: "a.wav", ResponseFormat: "json"})

	resp, err := s.handleInference(context.Background(), InferencePayload{
		ModelID: "openai/gpt-4o-transcribe", Operation: OpTranscriptions, Request: req,
	})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(resp.Response, &out)
	if out.Text != "ask not what your country can do for you" {
		t.Errorf("text = %q", out.Text)
	}
	if in, o := extractTokenCounts(resp.Response); in != 110 || o != 28 {
		t.Errorf("usage = %d/%d, want 110/28", in, o)
	}
	if sessionModel != realtimeSessionModel || txModel != "gpt-4o-transcribe" {
		t.Errorf("session model %q, transcription model %q", sessionModel, txModel)
	}
	if audioBytes != 2*12000 {
		t.Errorf("streamed %d bytes of audio, want %d", audioBytes, 2*12000)
	}
	for _, e := range events {
		if e == "response.create" {
			t.Error("the bridge asked the model for a reply; it only needs the transcript")
		}
	}
}

// The local gateway accepts the standard multipart upload.
func TestLocalGatewayTranscriptionMultipart(t *testing.T) {
	upgrader := websocket.Upgrader{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var ev map[string]any
			if conn.ReadJSON(&ev) != nil {
				return
			}
			if ev["type"] == "input_audio_buffer.commit" {
				_ = conn.WriteJSON(map[string]any{"type": "conversation.item.input_audio_transcription.completed", "transcript": "hello", "usage": map[string]any{"input_tokens": 5, "output_tokens": 1}})
			}
		}
	}))
	defer backend.Close()
	s, _ := newGatewayService(t, func(w http.ResponseWriter, r *http.Request) {})
	s.modelMappings["openai/gpt-4o-transcribe"] = ModelMapping{Endpoint: backend.URL, LocalModel: "gpt-4o-transcribe", Category: "transcription"}
	gw := httptest.NewServer(s.LocalGatewayHandler())
	defer gw.Close()

	var body bytes.Buffer
	mw := newMultipart(&body, map[string]string{"model": "openai/gpt-4o-transcribe", "response_format": "text"}, wavBytes(1, 16000, make([]int16, 1600)))
	resp, err := http.Post(gw.URL+"/v1/audio/transcriptions", mw, &body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(got) != "hello" {
		t.Fatalf("status %d body %q", resp.StatusCode, got)
	}
	if rec := lastLocalRecord(t, s); !rec.Success || rec.TokensIn != 5 || rec.Model != "openai/gpt-4o-transcribe" {
		t.Errorf("record = %+v", rec)
	}
}

// newMultipart writes a form with the given fields and an audio file, and
// returns its content type.
func newMultipart(body *bytes.Buffer, fields map[string]string, audio []byte) string {
	mw := multipart.NewWriter(body)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", "clip.wav")
	_, _ = fw.Write(audio)
	_ = mw.Close()
	return mw.FormDataContentType()
}
