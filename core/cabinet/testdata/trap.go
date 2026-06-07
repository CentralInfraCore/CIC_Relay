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

// Call is the main entry point for the module. It immediately traps.
//
//export Call
func Call(opPtr, opLen, authContextPtr, authContextLen, inputPtr, inputLen uint32) (packedResult uint64) {
	panic("intentional trap")
}
