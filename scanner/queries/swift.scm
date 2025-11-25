; Swift query for functions and imports

; Function declarations - capture the name
(function_declaration
  name: (simple_identifier) @function)

; Init declarations - no name field, so we skip them
; (they show as "init" which isn't very useful)

; Import statements - capture the identifier
(import_declaration
  (identifier
    (simple_identifier) @import))
