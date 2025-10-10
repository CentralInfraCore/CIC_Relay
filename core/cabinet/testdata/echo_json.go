//go:build wasip1

package main

// #include <stdlib.h>
import "C"
import "unsafe"

// main is required by the compiler, but we don't want it to run automatically.
func main() {}

//export allocate
func allocate(size uint32) uintptr {
	return uintptr(C.malloc(C.size_t(size)))
}

//export deallocate
func deallocate(ptr uintptr, size uint32) {
	C.free(unsafe.Pointer(ptr))
}

// Call is the main entry point for the module. It returns a hardcoded JSON string
// based on the `op` parameter provided by the host.
//
//export Call
func Call(opPtr, opLen, authContextPtr, authContextLen, inputPtr, inputLen uint32) (packedResult uint64) {
	opBytes := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(opPtr))), opLen)
	op := string(opBytes)

	var resultStr string
	switch op {
	case "get_null_data":
		resultStr = `{"error":{"code":"GUEST_REJECTED"}}`
	case "get_null_error":
		resultStr = `{"data":{"status":"ok"}}`
	case "get_invalid_json":
		resultStr = `not-a-valid-json`
	default:
		resultStr = `{"error":{"code":"UNKNOWN_OP"}}`
	}

	resultBytes := []byte(resultStr)
	resultSize := uint32(len(resultBytes))

	// Allocate memory for the result and copy it in.
	resultPtr := allocate(resultSize)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(resultPtr)), resultSize), resultBytes)

	// Pack the pointer and length into a uint64
	return (uint64(resultPtr) << 32) | uint64(resultSize)
}
