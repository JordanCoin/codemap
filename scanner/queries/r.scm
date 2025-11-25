; R query for functions and imports

; Function definitions using <- assignment
(binary_operator
  lhs: (identifier) @function
  operator: "<-"
  rhs: (function_definition))

; Function definitions using = assignment
(binary_operator
  lhs: (identifier) @function
  operator: "="
  rhs: (function_definition))

; library() calls
(call
  function: (identifier) @_fn
  arguments: (arguments
    (argument
      value: (identifier) @import))
  (#eq? @_fn "library"))

; require() calls
(call
  function: (identifier) @_fn
  arguments: (arguments
    (argument
      value: (identifier) @import))
  (#eq? @_fn "require"))

; source() calls for file imports
(call
  function: (identifier) @_fn
  arguments: (arguments
    (argument
      value: (string) @import))
  (#eq? @_fn "source"))
