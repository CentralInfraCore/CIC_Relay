(module
  (memory (export "memory") 1)

  ;; Data segment for the result string: {"data":null}
  (data (i32.const 100) "{\"data\":null}")

  ;; Main call function
  (func (export "Call") (param $ptr i32) (param $len i32) (result i64)
    (i64.add
      (i64.extend_i32_u (i32.shl (i32.const 100) (i32.const 32))) ;; Pointer to start of data segment (100)
      (i64.extend_i32_u (i32.const 13)) ;; Length of the string (13 characters)
    )
  )

  ;; Dummy allocate and deallocate functions for ABI compatibility
  (func (export "allocate") (param $size i32) (result i32)
    (i32.const 0) ;; Return a dummy pointer
  )
  (func (export "deallocate") (param $ptr i32) (param $size i32))
)
