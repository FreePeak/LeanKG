<?php

class Auth {
    public function login($username, $password) {
        echo "Logging in";
        return $username === "admin" && $password === "secret";
    }
}
?>
