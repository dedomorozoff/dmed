<?php
// Fixture for TestRealXdebugSession: the breakpoint lands on line 7.
$items = ["alpha", "beta"];

function add($a, $b)
{
    $sum = $a + $b;
    return $sum;
}

echo "start\n";
$total = add(2, 3);
echo "total=$total\n";
