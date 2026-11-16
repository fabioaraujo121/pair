package fetch

// The pins. To update one: download the file, check its hash against what
// the publisher states (Hugging Face lists the SHA-256 of an LFS file in
// its API; GitHub releases carry no digest, so download twice and compare),
// and write the new values here with the date.

// Whisper models, from ggerganov/whisper.cpp on Hugging Face at a fixed
// commit. The hash is the one Hugging Face publishes for the file. The
// quantized large-v3-turbo is the default: a third of the size of the full
// file, the same accuracy class, and whisper-cli loads it as it is. The full
// file stays pinned for PAIR_WHISPER_MODEL.
var Whisper = []Pin{
	{
		Name:   "ggml-large-v3-turbo-q5_0.bin",
		URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/ggml-large-v3-turbo-q5_0.bin",
		Size:   574041195,
		SHA256: "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2", // 2026-11-16
	},
	{
		Name:   "ggml-large-v3-turbo.bin",
		URL:    "https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/ggml-large-v3-turbo.bin",
		Size:   1624555275,
		SHA256: "1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69", // 2026-10-07
	},
}

// Kokoro model and voices, from the model-files-v1.0 release of
// thewh1teagle/kokoro-onnx, which kokoro-onnx itself points to.
var Kokoro = []Pin{
	{
		Name:   "kokoro-v1.0.onnx",
		URL:    kokoroRelease + "kokoro-v1.0.onnx",
		Size:   325532387,
		SHA256: "7d5df8ecf7d4b1878015a32686053fd0eebe2bc377234608764cc0ef3636a6c5", // 2026-10-07
	},
	{ // smaller and a little faster, for PAIR_KOKORO_MODEL
		Name:   "kokoro-v1.0.int8.onnx",
		URL:    kokoroRelease + "kokoro-v1.0.int8.onnx",
		Size:   92361271,
		SHA256: "6e742170d309016e5891a994e1ce1559c702a2ccd0075e67ef7157974f6406cb", // 2026-10-07
	},
	{
		Name:   "kokoro-v1.0.fp16.onnx",
		URL:    kokoroRelease + "kokoro-v1.0.fp16.onnx",
		Size:   177464787,
		SHA256: "c1610a859f3bdea01107e73e50100685af38fff88f5cd8e5c56df109ec880204", // 2026-10-07
	},
	{
		Name:   "voices-v1.0.bin",
		URL:    kokoroRelease + "voices-v1.0.bin",
		Size:   28214398,
		SHA256: "bca610b8308e8d99f32e6fe4197e7ec01679264efed0cac9140fe9c29f1fbf7d", // 2026-10-07
	},
}

const kokoroRelease = "https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/"
