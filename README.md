![ollama](https://github.com/jmorganca/ollama/assets/251292/961f99bb-251a-4eec-897d-1ba99997ad0f)

# Ollama

Run large language models with `llama.cpp`.

> Note: certain models that can be run with this project are intended for research and/or non-commercial use only.

### Features

- Download and run popular large language models
- Switch between multiple models on the fly
- Hardware acceleration where available (Metal, CUDA)
- Fast inference server written in Go, powered by [llama.cpp](https://github.com/ggerganov/llama.cpp)
- REST API to use with your application (python, typescript SDKs coming soon)

## Install

- Download for macOS
- Download for Windows (coming soon)
- Docker: `docker run -p 11434:11434 ollama/ollama`

You can also build the [binary from source](#building).

## Quickstart

Run the model that started it all.

```
ollama run llama
```

## Example models

### 💬 Chat

Have a conversation.

```
ollama run vicuna "Why is the sky blue?"
```

### 🗺️ Instructions

Ask questions. Get answers.

```
ollama run orca "Write an email to my boss."
```

### 📖 Storytelling

Venture into the unknown.

```
ollama run nous-hermes "Once upon a time"
```

## Advanced usage

### Run a local model

```
ollama run ~/Downloads/vicuna-7b-v1.3.ggmlv3.q4_1.bin
```

## Building

```
make
```

To run it start the server:

```
./ollama server &
```

Finally, run a model!

```
./ollama run ~/Downloads/vicuna-7b-v1.3.ggmlv3.q4_1.bin
```

## API Reference

### `POST /api/pull`

Download a model

```
curl -X POST http://localhost:11343/api/pull -d '{"model": "orca"}'
```

### `POST /api/generate`

Complete a prompt

```
curl -X POST http://localhost:11434/api/generate -d '{"model": "orca", "prompt": "hello!", "stream": true}'
```
