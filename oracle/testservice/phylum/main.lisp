;; Copyright © 2025 Luther Systems, Ltd. All right reserved.

;; main.lisp

(in-package 'test)
(use-package 'router)
(use-package 'utils)

;; service-name can be used to identify the service in health checks and longs.
(set 'service-name "test-cc")

(set 'version "LUTHER_PROJECT_VERSION")  ; overridden during build
(set 'build-id "LUTHER_PROJECT_BUILD_ID")  ; overridden during build
(set 'service-version (format-string "{} ({})" version build-id))

(set 'phylum-config "bootstrap-cfg")

(defun bootstrap() 
  (let* ([bootstrap-b64 (appctrl:get-prop phylum-config)])
    (when bootstrap-b64
      (let* ([bootstrap-json (to-string (base64:decode bootstrap-b64))] 
             [bootstrap-cfg (json:load-string bootstrap-json)]) 
        bootstrap-cfg))))

(defendpoint "get_config" (req)
  (route-success (bootstrap)))
