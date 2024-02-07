package convert

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/d4l3k/go-bfloat16"
	"github.com/mitchellh/mapstructure"
	"github.com/pdevine/tensor"
	"github.com/pdevine/tensor/native"
	"github.com/x448/float16"
	"google.golang.org/protobuf/proto"

	"github.com/ollama/ollama/convert/sentencepiece"
	"github.com/ollama/ollama/llm"
)

type Params struct {
	Architectures    []string `json:"architectures"`
	VocabSize        int      `json:"vocab_size"`
	HiddenSize       int      `json:"hidden_size"`       // n_embd
	HiddenLayers     int      `json:"num_hidden_layers"` // n_layer
	ContextSize      int      `json:"max_position_embeddings"`
	IntermediateSize int      `json:"intermediate_size"`
	AttentionHeads   int      `json:"num_attention_heads"` // n_head
	KeyValHeads      int      `json:"num_key_value_heads"`
	NormEPS          float64  `json:"rms_norm_eps"`
	RopeFreqBase     float64  `json:"rope_theta"`
	BoSTokenID       int      `json:"bos_token_id"`
	EoSTokenID       int      `json:"eos_token_id"`
	HeadDimension    int      `json:"head_dim"`
	PaddingTokenID   int      `json:"pad_token_id"`

	ByteOrder
}

type ByteOrder interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

type MetaData struct {
	Type    string `mapstructure:"dtype"`
	Shape   []int  `mapstructure:"shape"`
	Offsets []int  `mapstructure:"data_offsets"`
}

func ReadSafeTensors(fn string, offset uint64, params *Params) ([]llm.Tensor, uint64, error) {
	f, err := os.Open(fn)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	var jsonSize uint64
	if err := binary.Read(f, binary.LittleEndian, &jsonSize); err != nil {
		return nil, 0, err
	}

	buf := make([]byte, jsonSize)
	_, err = io.ReadFull(f, buf)
	if err != nil {
		return nil, 0, err
	}

	d := json.NewDecoder(bytes.NewBuffer(buf))
	d.UseNumber()
	var parsed map[string]interface{}
	if err = d.Decode(&parsed); err != nil {
		return nil, 0, err
	}

	var keys []string
	for k := range parsed {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	slog.Info("converting layers")

	var tensors []llm.Tensor
	for _, k := range keys {
		vals := parsed[k].(map[string]interface{})
		var data MetaData
		if err = mapstructure.Decode(vals, &data); err != nil {
			return nil, 0, err
		}

		var size uint64
		var kind uint32
		switch len(data.Shape) {
		case 0:
			// metadata
			continue
		case 1:
			// convert to float32
			kind = 0
			size = uint64(data.Shape[0] * 4)
		case 2:
			// convert to float16
			kind = 1
			size = uint64(data.Shape[0] * data.Shape[1] * 2)
		}

		ggufName, err := GetTensorName(k)
		if err != nil {
			slog.Error("%v", err)
			return nil, 0, err
		}

		shape := []uint64{0, 0, 0, 0}
		for i := range data.Shape {
			shape[i] = uint64(data.Shape[i])
		}

		t := llm.Tensor{
			Name:   ggufName,
			Kind:   kind,
			Offset: offset,
			Shape:  shape[:],
		}

		t.WriterTo = safetensorWriterTo{
			t:           &t,
			params:      params,
			bo:          params.ByteOrder,
			headCount:   uint32(params.AttentionHeads),
			headCountKV: uint32(params.KeyValHeads),
			filename:    fn,
			start:       uint64(data.Offsets[0]),
			end:         uint64(data.Offsets[1]),
			padding:     8 + jsonSize,
		}

		slog.Debug(fmt.Sprintf("%v", t))
		tensors = append(tensors, t)
		offset += size
	}
	return tensors, offset, nil
}

func GetSafeTensors(dirpath string, params *Params) ([]llm.Tensor, error) {
	var tensors []llm.Tensor
	files, err := filepath.Glob(filepath.Join(dirpath, "/model-*.safetensors"))
	if err != nil {
		return nil, err
	}

	var offset uint64
	for _, f := range files {
		var t []llm.Tensor
		var err error
		t, offset, err = ReadSafeTensors(f, offset, params)
		if err != nil {
			slog.Error("%v", err)
			return nil, err
		}
		tensors = append(tensors, t...)
	}
	return tensors, nil
}

func GetParams(dirpath string) (*Params, error) {
	f, err := os.Open(filepath.Join(dirpath, "config.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var params Params

	d := json.NewDecoder(f)
	err = d.Decode(&params)
	if err != nil {
		return nil, err
	}

	params.ByteOrder = binary.LittleEndian
	return &params, nil
}

// Details on gguf's tokenizer can be found at:
// https://github.com/ggerganov/ggml/blob/master/docs/gguf.md#tokenizer
type Vocab struct {
	Tokens []string
	Scores []float32
	Types  []int32
}

func LoadTokens(dirpath string, params *Params) (*Vocab, error) {
	slog.Info(fmt.Sprintf("reading vocab from %s", filepath.Join(dirpath, "tokenizer.model")))
	in, err := os.ReadFile(filepath.Join(dirpath, "tokenizer.model"))
	if err != nil {
		return nil, err
	}

	// To regenerate sentencepiece from the protobufs use:
	// protoc -I=./ --go_out=./ sentencepiece_model.proto
	modelProto := &sentencepiece.ModelProto{}
	if err := proto.Unmarshal(in, modelProto); err != nil {
		return nil, err
	}

	v := &Vocab{
		Tokens: make([]string, 0),
		Scores: make([]float32, 0),
		Types:  make([]int32, 0),
	}

	pieces := modelProto.GetPieces()
	for _, p := range pieces {
		v.Tokens = append(v.Tokens, p.GetPiece())
		v.Scores = append(v.Scores, p.GetScore())
		t := p.GetType()
		switch t {
		case sentencepiece.ModelProto_SentencePiece_UNKNOWN:
		case sentencepiece.ModelProto_SentencePiece_CONTROL:
		case sentencepiece.ModelProto_SentencePiece_UNUSED:
		case sentencepiece.ModelProto_SentencePiece_BYTE:
		default:
			t = sentencepiece.ModelProto_SentencePiece_NORMAL
		}
		v.Types = append(v.Types, int32(t))
	}

	slog.Info(fmt.Sprintf("vocab size: %d", len(v.Tokens)))

	// add any additional tokens
	addIn, err := os.ReadFile(filepath.Join(dirpath, "added_tokens.json"))
	if os.IsNotExist(err) {
		return v, nil
	} else if err != nil {
		return nil, err
	}

	slog.Info("reading user defined tokens")

	var extraTokenData map[string]int
	if err := json.Unmarshal(addIn, &extraTokenData); err != nil {
		return nil, err
	}

	type token struct {
		key string
		pos int
	}

	extraTokens := make([]token, 0)
	for k, id := range extraTokenData {
		extraTokens = append(extraTokens, token{k, id})
	}

	slices.SortFunc(extraTokens, func(a, b token) int {
		return cmp.Compare(a.pos, b.pos)
	})

	numToks := len(v.Tokens)

	for cnt, t := range extraTokens {
		// the token id should match the specific index for the total number of tokens
		if t.pos != cnt+numToks {
			return nil, fmt.Errorf("token ID '%d' for '%s' doesn't match total token size", t.pos, t.key)
		}
		v.Tokens = append(v.Tokens, t.key)
		v.Scores = append(v.Scores, -1000.0)
		v.Types = append(v.Types, int32(llm.GGUFTokenUserDefined))
	}
	slog.Info(fmt.Sprintf("vocab size w/ extra tokens: %d", len(v.Tokens)))

	if params.VocabSize > len(v.Tokens) {
		missingTokens := params.VocabSize - len(v.Tokens)
		slog.Warn(fmt.Sprintf("vocab is missing %d tokens", missingTokens))
		for cnt := 0; cnt < missingTokens; cnt++ {
			v.Tokens = append(v.Tokens, fmt.Sprintf("<dummy%05d>", cnt+1))
			v.Scores = append(v.Scores, -1)
			v.Types = append(v.Types, int32(llm.GGUFTokenUserDefined))
		}
	}

	return v, nil
}

func GetTensorName(n string) (string, error) {
	tMap := map[string]string{
		"model.embed_tokens.weight":                           "token_embd.weight",
		"model.layers.(\\d+).input_layernorm.weight":          "blk.$1.attn_norm.weight",
		"model.layers.(\\d+).mlp.down_proj.weight":            "blk.$1.ffn_down.weight",
		"model.layers.(\\d+).mlp.gate_proj.weight":            "blk.$1.ffn_gate.weight",
		"model.layers.(\\d+).mlp.up_proj.weight":              "blk.$1.ffn_up.weight",
		"model.layers.(\\d+).post_attention_layernorm.weight": "blk.$1.ffn_norm.weight",
		"model.layers.(\\d+).self_attn.k_proj.weight":         "blk.$1.attn_k.weight",
		"model.layers.(\\d+).self_attn.o_proj.weight":         "blk.$1.attn_output.weight",
		"model.layers.(\\d+).self_attn.q_proj.weight":         "blk.$1.attn_q.weight",
		"model.layers.(\\d+).self_attn.v_proj.weight":         "blk.$1.attn_v.weight",
		"lm_head.weight":    "output.weight",
		"model.norm.weight": "output_norm.weight",
	}

	v, ok := tMap[n]
	if ok {
		return v, nil
	}

	// quick hack to rename the layers to gguf format
	for k, v := range tMap {
		re := regexp.MustCompile(k)
		newName := re.ReplaceAllString(n, v)
		if newName != n {
			return newName, nil
		}
	}

	return "", fmt.Errorf("couldn't find a layer name for '%s'", n)
}

type safetensorWriterTo struct {
	t *llm.Tensor

	params      *Params
	bo          ByteOrder
	headCount   uint32
	headCountKV uint32

	filename string

	start, end, padding uint64
}

func (r safetensorWriterTo) addOnes(data []float32) ([]float32, error) {
	n := tensor.New(tensor.WithShape(int(r.t.Shape[0])), tensor.WithBacking(data))
	ones := tensor.Ones(tensor.Float32, int(r.t.Shape[0]))

	var err error
	n, err = n.Add(ones)
	if err != nil {
		return []float32{}, err
	}

	newN, err := native.SelectF32(n, 0)
	if err != nil {
		return []float32{}, err
	}

	var fullTensor []float32
	for _, v := range newN {
		fullTensor = append(fullTensor, v...)
	}

	return fullTensor, nil
}

func (r safetensorWriterTo) repack(data []uint16, heads int) ([]uint16, error) {
	n := tensor.New(tensor.WithShape(int(r.t.Shape[0]), int(r.t.Shape[1])), tensor.WithBacking(data))
	origShape := n.Shape().Clone()

	// reshape the tensor and swap axes 1 and 2 to unpack the layer for gguf
	if err := n.Reshape(heads, 2, origShape[0]/heads/2, origShape[1]); err != nil {
		return nil, err
	}

	if err := n.T(0, 2, 1, 3); err != nil {
		return nil, err
	}

	if err := n.Reshape(origShape...); err != nil {
		return nil, err
	}

	if err := n.Transpose(); err != nil {
		return nil, err
	}
	newN, err := native.SelectU16(n, 1)
	if err != nil {
		return nil, err
	}

	var fullTensor []uint16
	for _, v := range newN {
		fullTensor = append(fullTensor, v...)
	}
	return fullTensor, nil
}

func (r safetensorWriterTo) WriteTo(w io.Writer) (n int64, err error) {
	arch, err := getArchFromParams(r.params)
	if err != nil {
		return 0, err
	}

	f, err := os.Open(r.filename)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	if _, err = f.Seek(int64(r.padding+r.start), 0); err != nil {
		return 0, err
	}

	switch arch {
	case "llama":

		pattern := `^blk\.[0-9]+\.attn_(?P<layer>q|k)\.weight$`
		re, err := regexp.Compile(pattern)
		if err != nil {
			return 0, err
		}

		matches := re.FindAllStringSubmatch(r.t.Name, -1)
		if len(matches) > 0 {
			layerSize := r.end - r.start

			var err error
			tData := make([]uint16, layerSize/2)
			if err = binary.Read(f, r.bo, tData); err != nil {
				return 0, err
			}

			layerType := matches[0][re.SubexpIndex("layer")]
			var heads uint32
			switch layerType {
			case "q":
				heads = r.headCount
			case "k":
				heads = r.headCountKV
				if heads == 0 {
					heads = r.headCount
				}
			}

			tData, err = r.repack(tData, int(heads))
			if err != nil {
				return 0, err
			}

			var buf []byte
			for _, n := range tData {
				buf = r.bo.AppendUint16(buf, n)
			}

			tempBuf := make([]uint16, len(tData))
			tDataF32 := bfloat16.DecodeFloat32(buf)
			for cnt, v := range tDataF32 {
				tDataF16 := float16.Fromfloat32(v)
				tempBuf[cnt] = uint16(tDataF16)
			}

			if err = binary.Write(w, r.bo, tempBuf); err != nil {
				return 0, err
			}

			return 0, nil
		}

	case "gemma":
		if strings.HasSuffix(r.t.Name, "norm.weight") {
			slog.Debug(fmt.Sprintf("converting '%s'", r.t.Name))

			data := make([]byte, r.end-r.start)
			if err = binary.Read(f, r.bo, data); err != nil {
				return 0, err
			}

			tDataF32 := bfloat16.DecodeFloat32(data)

			var err error
			tDataF32, err = r.addOnes(tDataF32)
			if err != nil {
				return 0, err
			}

			if err := binary.Write(w, r.bo, tDataF32); err != nil {
				return 0, err
			}
			return 0, nil
		}
	}

	remaining := r.end - r.start

	bufSize := uint64(10240)
	var finished bool
	for {
		data := make([]byte, min(bufSize, remaining))

		b, err := io.ReadFull(f, data)
		remaining -= uint64(b)

		if err == io.EOF || remaining <= 0 {
			finished = true
		} else if err != nil {
			return 0, err
		}

		// convert bfloat16 -> ieee float32
		tDataF32 := bfloat16.DecodeFloat32(data)

		switch r.t.Kind {
		case 0:
			if err := binary.Write(w, r.bo, tDataF32); err != nil {
				return 0, err
			}
		case 1:
			// convert float32 -> float16
			tempBuf := make([]uint16, len(data)/2)
			for cnt, v := range tDataF32 {
				tDataF16 := float16.Fromfloat32(v)
				tempBuf[cnt] = uint16(tDataF16)
			}
			if err := binary.Write(w, binary.LittleEndian, tempBuf); err != nil {
				return 0, err
			}
		}
		if finished {
			break
		}
	}
	return 0, nil
}

func getArchFromParams(params *Params) (string, error) {
	var arch string
	switch len(params.Architectures) {
	case 0:
		return "", fmt.Errorf("No architecture specified to convert")
	case 1:
		switch params.Architectures[0] {
		case "MistralForCausalLM":
			arch = "llama"
		case "GemmaForCausalLM":
			arch = "gemma"
		default:
			return "", fmt.Errorf("Models based on '%s' are not yet supported", params.Architectures[0])
		}
	default:
		return "", fmt.Errorf("Multimodal models are not yet supported")
	}

	return arch, nil
}

func WriteGGUF(name string, tensors []llm.Tensor, params *Params, vocab *Vocab) (string, error) {
	arch, err := getArchFromParams(params)
	if err != nil {
		return "", err
	}

	kv := llm.KV{
		"general.architecture": arch,
		"general.name":         name,
	}

	switch arch {
	case "llama":
		kv["llama.context_length"] = uint32(params.ContextSize)
		kv["llama.embedding_length"] = uint32(params.HiddenSize)
		kv["llama.block_count"] = uint32(params.HiddenLayers)
		kv["llama.feed_forward_length"] = uint32(params.IntermediateSize)
		kv["llama.rope.dimension_count"] = uint32(params.HiddenSize / params.AttentionHeads)
		slog.Debug(fmt.Sprintf("rope dim count = %d", kv["llama.rope.dimension_count"]))
		kv["llama.attention.head_count"] = uint32(params.AttentionHeads)
		kv["llama.attention.head_count_kv"] = uint32(params.KeyValHeads)
		kv["llama.attention.layer_norm_rms_epsilon"] = float32(params.NormEPS)
		kv["llama.rope.freq_base"] = float32(params.RopeFreqBase)
	case "gemma":
		kv["gemma.context_length"] = uint32(params.ContextSize)
		kv["gemma.embedding_length"] = uint32(params.HiddenSize)
		kv["gemma.block_count"] = uint32(params.HiddenLayers)
		kv["gemma.feed_forward_length"] = uint32(params.IntermediateSize)
		kv["gemma.attention.head_count"] = uint32(params.AttentionHeads)
		kv["gemma.attention.head_count_kv"] = uint32(params.KeyValHeads)
		kv["gemma.attention.layer_norm_rms_epsilon"] = float32(params.NormEPS)
		kv["gemma.attention.key_length"] = uint32(params.HeadDimension)
		kv["gemma.attention.value_length"] = uint32(params.HeadDimension)
	}

	kv["general.file_type"] = uint32(1)
	kv["tokenizer.ggml.model"] = "llama"

	kv["tokenizer.ggml.tokens"] = vocab.Tokens
	kv["tokenizer.ggml.scores"] = vocab.Scores
	kv["tokenizer.ggml.token_type"] = vocab.Types

	kv["tokenizer.ggml.bos_token_id"] = uint32(params.BoSTokenID)
	kv["tokenizer.ggml.eos_token_id"] = uint32(params.EoSTokenID)

	switch arch {
	case "llama":
		kv["tokenizer.ggml.unknown_token_id"] = uint32(0)
	case "gemma":
		kv["tokenizer.ggml.padding_token_id"] = uint32(params.PaddingTokenID)
		kv["tokenizer.ggml.unknown_token_id"] = uint32(3)
	}

	kv["tokenizer.ggml.add_bos_token"] = true
	kv["tokenizer.ggml.add_eos_token"] = false

	f, err := os.CreateTemp("", "ollama-gguf")
	if err != nil {
		return "", err
	}
	defer f.Close()

	m := llm.NewGGUFV3(params.ByteOrder)
	if err := m.Encode(f, kv, tensors); err != nil {
		return "", err
	}

	return f.Name(), nil
}
