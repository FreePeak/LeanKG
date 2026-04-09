<?php

require_once 'Auth.php';

class AuthTest {
    public function testLogin() {
        $auth = new Auth();
        assert($auth->login("admin", "secret") === true);
    }
}
?>
