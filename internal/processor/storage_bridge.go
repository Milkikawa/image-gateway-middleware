package processor

import "image-gateway-middleware/internal/storage"

func storageFree(path string) (uint64, error) { return storage.FreeBytes(path) }
