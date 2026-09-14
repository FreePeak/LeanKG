<?php

interface Greetable
{
}

trait Greets
{
}

final class Greeter implements Greetable
{
    use Greets;

    public static function greet(): string
    {
        return 'hi';
    }
}
