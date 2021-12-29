(use-package 'testing)

(test-let "basic"
  ((val (handlebars:render """
<div class="entry">
  <h1>{{title}}</h1>
  <div class="body">
    {{body}}
  </div>
</div>
""" (json:dump-bytes
      (sorted-map "title" "My New Post"
                  "body" "This is my first post!")))))
  (assert-string= """
<div class="entry">
  <h1>My New Post</h1>
  <div class="body">
    This is my first post!
  </div>
</div>
""" val))

(test-let "missing"
  ((val (handlebars:render """
<div class="entry">
  <h1>{{title}}</h1>
  <div class="body">
    {{body}}
  </div>
  <div>{{mystery}}</div>
</div>
""" (json:dump-bytes
      (sorted-map "title" "My New Post"
                  "body" "This is my first post!")))))
  (assert-string= """
<div class="entry">
  <h1>My New Post</h1>
  <div class="body">
    This is my first post!
  </div>
  <div></div>
</div>
""" val))

(test-let "books"
  ((val (handlebars:render """
{{users.marcel.books.book1}}
{{users.marcel.books.book2}}
{{users.didier.[0].book}}
{{users.didier.[1].book}}
""" (json:dump-bytes
      (sorted-map "users"
                  (sorted-map "marcel"
                              (sorted-map "books"
                                          (sorted-map "book1"
                                                      "My first book"
                                                      "book2"
                                                      "My second book"))
                              "didier"
                              (list (sorted-map "book"
                                                "Good book")
                                    (sorted-map "book"
                                                "Bad book"))))))))
  (assert-string= """
My first book
My second book
Good book
Bad book
""" val))

(test-let "in-string-array-exist"
  ((
    val (
      handlebars:render """{{ in-string-array haystack=array needle="pear"}}"""
        (sorted-map "array" (list "apple" "orange" "pear"))
      )
    ))
  (assert-string= val """true""")
  )

(test-let "in-string-array-non-exist"
  ((
    val (
      handlebars:render """{{ in-string-array haystack=array needle="kiwi"}}"""
       (sorted-map "array" (list "apple" "orange" "pear"))
      )
    ))
  (assert-string= val """false""")
  )

(test-let "array-length"
  ((
    val (
      handlebars:render """{{ len array }}"""
        (sorted-map "array" (list 1 2 3))
      )
    ))
  (assert-string= val """3""")
  )

(test-let "equality-numeric"
  ((
    val (
      handlebars:render """{{eq foo 1.2}}"""
        (sorted-map "foo" 1.2)
      )
    ))
  (assert-string= """true""" val)
  )

(test-let "equality-string"
  ((
    val (
      handlebars:render """{{eq foo "bar"}}"""
        (sorted-map "foo" "bar")
      )
    ))
  (assert-string= """true""" val)
  )

(test-let "greater-than"
  ((
    val (
      handlebars:render """{{#if (gt foo 2.2)}}yes{{/if}}"""
       (sorted-map "foo" 13.1)
      )
    ))
  (assert-string= """yes""" val)
  )

(test-let "greater-than-equal"
  ((
    val (
      handlebars:render """{{#if (gte foo 2.2)}}yes{{/if}}"""
       (sorted-map "foo" 2.2)
      )
    ))
  (assert-string= """yes""" val)
  )

(test-let "not"
  ((
    val (
      handlebars:render """{{ not foo }}"""
       (sorted-map "foo" true)
      )
    ))
  (assert-string= """false""" val)
  )

(test-let "and"
  ((
    val (
      handlebars:render """{{ and test1=true
                                test2=true
                                test3=false }}"""
        (sorted-map "foo" true)
      )
    ))
  (assert-string= """false""" val)
  )

(test-let "and-true"
  ((
    val (
      handlebars:render """{{ and test1=true
                                test2=true
                                test3=true }}"""
        (sorted-map "foo" true)
      )
    ))
  (assert-string= """true""" val)
  )

(test-let "or"
  ((
    val (
      handlebars:render """{{ or test1=foo
                               test2=false
                               test3=false }}"""
      (sorted-map "foo" true)
      )
    ))
  (assert-string= """true""" val)
  )

(test-let "or-false"
  ((
    val (
      handlebars:render """{{ or test1=false
                                test2=false
                                test3=false }}"""
        ()
      )
    ))
  (assert-string= """false""" val)
  )

(test-let "less-than"
  ((
    val (
      handlebars:render """{{#if (lt foo 13.1)}}yes{{/if}}"""
        (sorted-map "foo" 2)
      )
    ))
  (assert-string= """yes""" val)
  )

(test-let "less-than-equal"
  ((
    val (
      handlebars:render """{{#if (lte foo 2)}}yes{{/if}}"""
        (sorted-map "foo" 2)
      )
    ))
  (assert-string= """yes""" val)
  )

(test-let "embedded-if"
  ((
    val (
      handlebars:render """{{#if (and
                                    gt_cond=(gt (len array) 2)
                                    lt_cond=(lt (len array) 4)
                               )}}yes{{else}}no{{/if}}"""
        (sorted-map "array" (list 1 2 3))
      )
    ))
  (assert-string= """yes""" val)
  )

(test-let "times"
  ((
    val (
      handlebars:render """{{times foo foo}}"""
        (sorted-map "foo" 2)
      )
    ))
  (assert-string= """4""" val)
  )

(test-let "divide"
  ((
    val (
      handlebars:render """{{div foo 2}}"""
        (sorted-map "foo" 5)
      )
    ))
  (assert-string= """2.5""" val)
  )

(test-let "plus"
  ((
    val (
      handlebars:render """{{plus var=foo const1=2 const2=3}}"""
        (sorted-map "foo" 2)
      )
    ))
  (assert-string= """7""" val)
  )

(test-let "minus"
  ((
    val (
      handlebars:render """{{minus foo const1=2 const2=1}}"""
        (sorted-map "foo" 4)
      )
    ))
  (assert-string= """1""" val)
  )

(test-let "mod"
  ((
    val (
      handlebars:render """{{mod foo 2}}"""
        (sorted-map "foo" 3)
      )
    ))
  (assert-string= """1""" val)
  )

(test-let "books-each"
  ((val (handlebars:render """
{{#each users as |user userId|}}
  {{#each user.books as |book bookId|}}
    User: {{userId}} Book: {{bookId}}
  {{/each}}
{{/each}}
""" (sorted-map "users"
                (sorted-map "marcel"
                            (sorted-map "books"
                                        (sorted-map "book1"
                                                    "My first book"
                                                    "book2"
                                                    "My second book"))
                            "didier"
                            (sorted-map "books"
                                        (sorted-map "bookA"
                                                    "Good book"
                                                    "bookB"
                                                    "Bad book")))))))
  (assert-string= val """
    User: didier Book: bookA
    User: didier Book: bookB
    User: marcel Book: book1
    User: marcel Book: book2
"""))

(test-let "no-log"
  ((val (handlebars:render """
{{log "Look at me!" }}
""" ())))
  (assert-string= """

""" val))

(test-let "no-lookup"
  ((val (handlebars:render """
{{#each goodbyes}}{{lookup ../data @index}}{{/each}}
""" (sorted-map "goodbyes" (list 0 1)
                "data" (list "foo" "bar")))))
  (assert-string= """

""" val))

;; SELECT string_val FROM metadata WHERE name=JWKS_URI
;; is equivalent to:
;; {{select "string_val" from=metadata where="name=JWKS_URI"}}

(test-let "select-from-where-ok"
  ((
    val (
      handlebars:render """{{#select from=metadata where="name=JWKS_URI"}}{{string_val}}{{/select}}"""
        (sorted-map "metadata"
                   (vector
                      (sorted-map "name" "AUTH_NAME" "string_val" "Luther")
                      (sorted-map "name" "JWKS_URI" "string_val" "www.luthersystems.com")
                      (sorted-map "name" "ENABLED" "bool_val" true)))
       )
    ))
  (assert-string= """www.luthersystems.com""" val)
  )

(test-let "select-from-where-empty"
  ((
    val (
      handlebars:render """{{#select from=metadata where="name=JWKS_URI"}}{{string_val}}{{/select}}"""
        (sorted-map "metadata" (vector))
      )
    ))
  (assert-string= "" val)
  )

(test-let "select-from-where-2ok"
  ((
    val (
      handlebars:render """{{#select from=metadata where="name=JWKS_URI"}}{{string_val}}{{/select}}"""
        (sorted-map "metadata"
                    (vector
                      (sorted-map "name" "AUTH_NAME" "string_val" "Luther")
                      (sorted-map "name" "JWKS_URI" "string_val" "www.luthersystems.com")
                      (sorted-map "name" "ENABLED" "bool_val" true)
                       (sorted-map "name" "JWKS_URI" "string_val" "/")))
        )
    ))
  (assert-string= """www.luthersystems.com/""" val)
  )

(test-let "global-vars"
  ((
    val (
      handlebars:render """{{global "testns1" key="INVALID" val="Invalid"}}{{global"testns1" key=tKey}}{{global "testns2" key=tKey}}"""
        (sorted-map "tKey" "INVALID")
      )
    ))
  (assert-string= """Invalid""" val)
  )

(test-let "global-vars-loop"
  ((
    val (
      handlebars:render """{{global "testns1" key="INVALID" val="Invalid"}}{{#each metadata}}{{global "testns1" key=tKey}}{{/each}}"""
        (sorted-map
           "tKey" "INVALID"
           "metadata" (vector (sorted-map "name" "AUTH_NAME" "string_val" "Luther")
                              (sorted-map "name" "JWKS_URI" "string_val" "www.luthersystems.com")
                              (sorted-map "name" "ENABLED" "bool_val" true)
                              (sorted-map "name" "JWKS_URI" "string_val" "/")))
         )
    ))
  (assert-string= """InvalidInvalidInvalidInvalid""" val)
  )

(test-let "must-parse-ok"
  ((
    val (handlebars:must-parse """{{#if (lt foo 13.1)}}yes{{/if}}""")
    ))
  (assert-equal () val)
  )

(test "must-parse-error"
  (handler-bind ((handlebars-parse (lambda (c &rest _))))
                (handlebars:must-parse """{{#if (lt foo 13.1)}}yes{/if}}""")))

(test-let "after-same"
  ((
    val (
         handlebars:render """{{is-after date ref-date}}"""
         (sorted-map "date" "2019-10-26"
                     "ref-date" "2019-10-26")
         )
    ))
  (assert-string= """false""" val)
  )
(test-let "after-before"
  ((
    val (
         handlebars:render """{{is-after date ref-date}}"""
         (sorted-map "date" "2019-09-26"
                     "ref-date" "2019-10-26")
         )
    ))
  (assert-string= """false""" val)
  )
(test-let "after-after"
  ((
    val (
         handlebars:render """{{is-after date ref-date}}"""
         (sorted-map "date" "2020-03-20"
                     "ref-date" "2019-10-26")
         )
    ))
  (assert-string= """true""" val)
  )

(test-let "date-diff-month-same"
  ((
    val (
         handlebars:render """{{date-diff-month date1 date2}}"""
         (sorted-map "date1" "2019-10-26"
                     "date2" "2019-10-26")
         )
    ))
  (assert-string= """0""" val)
  )
(test-let "date-diff-month-before"
  ((
    val (
         handlebars:render """{{date-diff-month date1 date2}}"""
         (sorted-map "date1" "2020-03-20"
                     "date2" "2019-10-26")
         )
    ))
  (assert-string= """5""" val)
  )
(test-let "date-diff-month-after"
  ((
    val (
         handlebars:render """{{date-diff-month date1 date2}}"""
         (sorted-map "date1" "2019-10-26"
                     "date2" "2020-03-20")
         )
    ))
  (assert-string= """5""" val)
  )
(test-let "date-add-next-year"
  ((
    val (
         handlebars:render """{{date-add-months date 1}}"""
         (sorted-map "date" "2019-12-26")
         )
    ))
  (assert-string= """2020-01-26""" val)
  )
(test-let "date-add-two-months"
  ((
    val (
         handlebars:render """{{date-add-months date 2}}"""
         (sorted-map "date" "2019-10-26")
         )
    ))
  (assert-string= """2019-12-26""" val)
  )
(test-let "date-add-one-month-shorter"
  ((
    val (
         handlebars:render """{{date-add-months date 1}}"""
         (sorted-map "date" "2019-01-31")
         )
    ))
  (assert-string= """2019-03-03""" val)
  )
(test-let "date-add-one-month-negative"
  ((
    val (
         handlebars:render """{{date-add-months date -1}}"""
         (sorted-map "date" "2019-01-31")
         )
    ))
  (assert-string= """2018-12-31""" val)
  )

(test-let "round-to-nth-1"
  ((
    val (
         handlebars:render """{{round-to-nth x n}}"""
         (sorted-map "x" 1.999 "n" 2)
         )
    ))
  (assert-string= """2.00""" val)
  )

(test-let "round-to-nth-2"
  ((
    val (
         handlebars:render """{{round-to-nth x n}}"""
         (sorted-map "x" "1.0001" "n" 2)
         )
    ))
  (assert-string= """1.00""" val)
  )

;; prettyp-num-en tests

(test "prettyp-num-en-integer"
  (assert-string=
    "200,000.00"
    (handlebars:render """{{prettyp-num-en 200000}}""" (sorted-map "foo" "bar"))))

(test "prettyp-num-en-decimal"
  (assert-string=
    "200,000.20"
    (handlebars:render """{{prettyp-num-en 200000.2}}""" (sorted-map "foo" "bar"))))

(test "prettyp-num-en-decimal-large-number"
  (assert-string=
    "21,543,000.20"
    (handlebars:render """{{prettyp-num-en 21543000.2}}""" (sorted-map "foo" "bar"))))

(test "prettyp-num-en-2dp"
  (assert-string=
    "0.20"
    (handlebars:render """{{prettyp-num-en 0.2}}""" (sorted-map "foo" "bar"))))

(test "prettyp-num-en-2dp-rounding"
  (assert-string=
    "0.21"
    (handlebars:render """{{prettyp-num-en 0.205}}""" (sorted-map "foo" "bar"))))

(test "prettyp-num-en-zeros"
  (assert-string=
    "0.00"
    (handlebars:render """{{prettyp-num-en 0000}}""" (sorted-map "foo" "bar"))))

(test "prettyp-num-en-negative"
  (assert-string=
    "-76,543.20"
    (handlebars:render """{{prettyp-num-en -76543.201}}""" (sorted-map "foo" "bar"))))


(test "prettyp-num-en-string-number"
  (assert-string=
    "1,212.12"
    (handlebars:render """{{prettyp-num-en "1212.12"}}""" (sorted-map "foo" "bar"))))

;; possessive tests

(test "possessive-ending-with-s"
  (assert-string=
    "friends'"
    (handlebars:render """{{{possessive name}}}""" (sorted-map "name" "friends"))))

(test "possessive-apostrophe-s"
  (assert-string=
    "David's"
    (handlebars:render """{{{possessive name}}}""" (sorted-map "name" "David"))))

(test "possessive-spaces-right"
  (assert-string=
    "David's"
    (handlebars:render """{{{possessive name}}}""" (sorted-map "name" "David   "))))

(test "possessive-full-name"
  (assert-string=
    "David Fincher's"
    (handlebars:render """{{{possessive name}}}""" (sorted-map "name" "David Fincher"))))

(test "possessive-empty"
  (assert-string=
    ""
    (handlebars:render """{{{possessive name}}}""" (sorted-map "name" ""))))

;; date-beautify tests

(test "date-beautify-standard"
  (assert-string=
    "30 January 2020"
    (handlebars:render """{{{date-beautify "2020-01-30"}}}""" (sorted-map "foo" "bar"))))

(test "date-beautify-DMY-not-MDY"
  (assert-string=
    "11 March 2020"
    (handlebars:render """{{{date-beautify "2020-03-11"}}}""" (sorted-map "foo" "bar"))))

(test "date-beautify-null"
  (assert-string=
    ""
    (handlebars:render """{{{date-beautify ""}}}""" (sorted-map "foo" "bar"))))

;; date-DDMMYY-slash tests

(test "date-DDMMYY-slash"
  (assert-string=
    "30/01/20"
    (handlebars:render """{{{date-DDMMYY-slash "2020-01-30"}}}""" (sorted-map "foo" "bar"))))

;; date-DDMMYYYY-slash tests

(test "date-DDMMYYYY-slash"
  (assert-string=
    "30/01/2020"
    (handlebars:render """{{{date-DDMMYYYY-slash "2020-01-30"}}}""" (sorted-map "foo" "bar"))))

;; date-DDMMYYYY tests

(test "date-DDMMYYYY"
  (assert-string=
    "30-01-2020"
    (handlebars:render """{{{date-DDMMYYYY "2020-01-30"}}}""" (sorted-map "foo" "bar"))))

;; format-phone-gb valid tests

(test "format-phone-gb-valid"
  (assert-string=
    "07709 789111"
    (handlebars:render """{{{format-phone-gb "+447709789111"}}}""" (sorted-map "foo" "bar"))))

;; format-phone-gb invalid tests

(test "format-phone-gb-invalid"
  (assert-string=
    "numberzz"
    (handlebars:render """{{{format-phone-gb "numberzz"}}}""" (sorted-map "foo" "bar"))))

;; format-phone-gb invalid tests

(test "format-phone-gb-null"
  (assert-string=
    ""
    (handlebars:render """{{{format-phone-gb ""}}}""" (sorted-map "foo" "bar"))))

;; format-phone-gb non-GB number tests

(test "format-phone-gb-US"
  (assert-string=
    "+18882378289"
    (handlebars:render """{{{format-phone-gb "+18882378289"}}}""" (sorted-map "foo" "bar"))))

;; format-phone-gb pre-formatted incorrectly tests

(test "format-phone-pre-formatted-without-country-code"
  (assert-string=
    "07709 789111"
    (handlebars:render """{{{format-phone-gb "07709-789-111"}}}""" (sorted-map "foo" "bar"))))

;; format-phone-gb Isle of Man tests

(test "format-phone-isle-of-man"
  (assert-string=
    "01624 861484"
    (handlebars:render """{{{format-phone-gb "1624861484"}}}""" (sorted-map "foo" "bar"))))

;; format-phone-gb Jersey tests

(test "format-phone-jersey"
  (assert-string=
    "01534 726278"
    (handlebars:render """{{{format-phone-gb "1534726278"}}}""" (sorted-map "foo" "bar"))))

;; escape uri test - email

(test
  "escape-uri-email"
  (assert-string=
    "hello%40example.com"
    (handlebars:render """{{{escape-uri-component "hello@example.com"}}}""" (sorted-map "foo" "bar"))))

(test
  "escape-uri-other"
  (assert-string=
    "%26+one+%25+two+%2F+three+"
    (handlebars:render """{{{escape-uri-component  "& one % two / three "}}}""" (sorted-map "foo" "bar"))))

;; to-str tests

(test "to-str-valid-string"
  (assert-string=
    "123"
    (handlebars:render """{{{to-str "123"}}}""" (sorted-map "foo" "bar"))))

(test "to-str-valid-uint"
  (assert-string=
    "123"
    (handlebars:render """{{{to-str 123}}}""" (sorted-map "foo" "bar"))))

(test "to-str-valid-int"
  (assert-string=
    "-123"
    (handlebars:render """{{{to-str -123}}}""" (sorted-map "foo" "bar"))))

(test "to-str-valid-float"
  (assert-string=
    "123.123000"
    (handlebars:render """{{{to-str 123.123}}}""" (sorted-map "foo" "bar"))))

(test "to-str-invalid-input"
  (assert-string=
    ""
    (handlebars:render """{{{to-str true}}}""" (sorted-map "foo" "bar"))))

(test-let "to-str-with-global-vars-and-plus"
  ((
    val (
      handlebars:render """{{global "custom" key="sum" val="0"}}{{global "custom" key="sum" val=(to-str (plus a=(global"custom" key="sum") b=100))}}{{prettyp-num-en (global"custom" key="sum")}}"""
        (sorted-map "foo" "bar")
      )
    ))
  (assert-string= """100.00""" val)
  )

;; to-int tests

(test "to-int-valid-float"
  (assert-string=
    "123"
    (handlebars:render """{{{to-int 123.123}}}""" (sorted-map "foo" "bar"))))

(test "to-int-valid-int"
  (assert-string=
    "123"
    (handlebars:render """{{{to-int 123}}}""" (sorted-map "foo" "bar"))))

(test "to-int-valid-string"
  (assert-string=
    "123"
    (handlebars:render """{{{to-int "123"}}}""" (sorted-map "foo" "bar"))))

(test-let "to-intdate-add-next-year-month-float"
  ((
    val (
         handlebars:render """{{date-add-months date (to-int 1.0)}}"""
         (sorted-map "date" "2019-12-26")
         )
    ))
  (assert-string= """2020-01-26""" val)
  )

(test-let "to-intdate-add-next-year-month-string"
  ((
    val (
         handlebars:render """{{date-add-months date (to-int "1")}}"""
         (sorted-map "date" "2019-12-26")
         )
    ))
  (assert-string= """2020-01-26""" val)
  )
