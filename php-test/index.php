<?php
// dmed PHP debug test project
function fib($n) {
    if ($n < 2) {
        return $n;
    }
    return fib($n - 1) + fib($n - 2);
}

echo "hello from dmed php-test\n";
for ($i = 0; $i < 10; $i++) {
    $f = fib($i);
    echo "fib($i) = $f\n";
}

$items = ["alpha", "beta", "gamma"];
foreach ($items as $k => $v) {
    echo "$k => $v\n";
}
echo "done\n";