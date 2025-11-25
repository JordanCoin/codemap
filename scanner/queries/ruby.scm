; Ruby query for functions and imports

; Method definitions
(method
  name: (identifier) @function)

; Singleton method definitions
(singleton_method
  name: (identifier) @function)

; require 'x'
(call
  method: (identifier) @_req (#match? @_req "^require")
  arguments: (argument_list (string) @import))

; require_relative 'x'
(call
  method: (identifier) @_req (#match? @_req "^require_relative")
  arguments: (argument_list (string) @import))
