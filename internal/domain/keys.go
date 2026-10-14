package domain

const (
	KDFVersionLegacy  = 0
	KDFVersionArgon2  = 1
	KDFSaltSize       = 16
	X25519KeySize     = 32
	KDFAlgorithm      = "argon2id"
	KDFMemoryKiB      = 65536
	KDFIterations     = 3
	KDFParallelism    = 1
	MaxWrappedKeySize = 1024
)

func ValidWrappedKey(blob []byte) bool {
	return len(blob) > 28 && len(blob) <= MaxWrappedKeySize
}
