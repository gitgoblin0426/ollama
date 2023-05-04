import json
import os
from llama_cpp import Llama
from flask import Flask, Response, stream_with_context, request
from flask_cors import CORS, cross_origin

app = Flask(__name__)
CORS(app)  # enable CORS for all routes

# llms tracks which models are loaded
llms = {}


@app.route("/generate", methods=["POST"])
def generate():
    data = request.get_json()
    model = data.get("model")
    prompt = data.get("prompt")

    if not model:
        return Response("Model is required", status=400)
    if not prompt:
        return Response("Prompt is required", status=400)
    if not os.path.exists(f"../models/{model}.bin"):
        return {"error": "The model file does not exist."}, 400

    if model not in llms:
        llms[model] = Llama(model_path=f"../models/{model}.bin")

    def stream_response():
        stream = llms[model](
            str(prompt),  # TODO: optimize prompt based on model
            max_tokens=4096,
            stop=["Q:", "\n"],
            echo=True,
            stream=True,
        )
        for output in stream:
            yield json.dumps(output)

    return Response(
        stream_with_context(stream_response()), mimetype="text/event-stream"
    )


if __name__ == "__main__":
    app.run(debug=True, threaded=True, port=5000)
