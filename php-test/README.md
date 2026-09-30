# php-test — manual fixture for the Xdebug/DBGp flow

`index.php` is **not** used by any automated test: `TestPHPXdebugBreakpointHit`
generates its own script in `t.TempDir()`. This file is a ready-made target for
checking PHP debugging by hand, which is worth keeping because the DBGp path is
the one flow that is hard to exercise without a real interpreter.

To use it:

```sh
php -d zend_extension=xdebug \
    -d xdebug.mode=debug \
    -d xdebug.start_with_request=yes \
    -d xdebug.client_port=9003 \
    index.php
```

Then in dmed open this file, press `F4` on a line (e.g. the `fib` body), and
`F5`. The panel should show a hit thread, a stack, and the variables.

Without Xdebug loaded the launch fails fast and the panel reports it, so a
silent no-op is not a symptom you have to interpret.

Note: this directory is intentionally outside `internal/`, so `go build ./...`
never sees it and CI does not need PHP installed.
