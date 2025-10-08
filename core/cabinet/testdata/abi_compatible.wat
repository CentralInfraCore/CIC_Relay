;; This module is designed to be compatible with CicWasmHost's ABI.
(module
  (memory (export "memory") 1)

  ;; Function to allocate memory
  ;; Takes size (i64), returns pointer (i64)
  (func (export "allocate") (param $size i64) (result i64)
    local.get $size ;; For a dummy implementation, just return the size as a pointer
  )

  ;; Function to deallocate memory
  ;; Takes pointer (i64) and size (i64)
  (func (export "deallocate") (param $ptr i64) (param $size i64)
    ;; No-op for this test module
  )

  ;; Function to handle the main call
  ;; Expects 6 i64 parameters (op_ptr, op_len, auth_ptr, auth_len, data_ptr, data_len)
  ;; Returns 1 i64 result (packed result_ptr, result_len)
  (func (export "Call")
    (param $op_ptr i64) (param $op_len i64)
    (param $auth_ptr i64) (param $auth_len i64)
    (param $data_ptr i64) (param $data_len i64)
    (result i64)
    ;; For this test, we'll return a dummy packed result (ptr=0, len=0)
    i64.const 0
  )
)
