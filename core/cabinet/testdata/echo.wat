(module
  (memory (export "memory") 1) ;; 1 page = 64KiB

  ;; A very simple bump allocator. We just track the end of the heap.
  (global $heap_base (mut i32) (i32.const 8)) ;; Start heap at 8 to be safe

  ;; allocate: (param i32) -> (result i32)
  ;; Allocates a block of memory of the given size and returns a pointer to it.
  (func $allocate (export "allocate") (param $size i32) (result i32)
    ;; Get the current heap base, this will be our pointer
    global.get $heap_base
    
    ;; Move the heap base pointer up by the requested size
    global.get $heap_base
    local.get $size
    i32.add
    global.set $heap_base
    
    ;; Return the original pointer
  )

  ;; echo: (param i32 i32) -> (result i32)
  ;; A test function that takes a pointer and a size, and simply returns the size.
  ;; This allows the host to verify that the parameters were passed correctly.
  (func $echo (export "echo") (param $ptr i32) (param $size i32) (result i32)
    ;; We don't do anything with the data at $ptr, we just return the $size
    ;; to prove we received it.
    local.get $size
  )
)
