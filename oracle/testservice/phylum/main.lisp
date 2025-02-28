;; Copyright © 2025 Luther Systems, Ltd. All right reserved.

;; main.lisp

(in-package 'test)
(use-package 'router)
(use-package 'utils)

;; service-name can be used to identify the service in health checks and longs.
(set 'service-name "ui-cc")

(set 'version "LUTHER_PROJECT_VERSION")  ; overridden during build
(set 'build-id "LUTHER_PROJECT_BUILD_ID")  ; overridden during build
(set 'service-version (format-string "{} ({})" version build-id))
