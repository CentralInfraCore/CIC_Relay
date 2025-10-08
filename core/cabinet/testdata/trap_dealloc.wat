(module
  (memory (export "memory") 1)
  (func (export "allocate") (param i64) (result i64) (i64.const 0))
  (func (export "deallocate") (param i64) (param i64)
    unreachable)
  (func (export "Call") (param $op_ptr i64) (param $op_len i64) (param $auth_ptr i64) (param $auth_len i64) (param $data_ptr i64) (param $data_len i64) (result i64)
    (i64.const 0))
)
