<?php
require_once 'vendor/autoload.php';

use App\Models\User;
use App\Repositories\UserRepository;

class UserService
{
    public function find($id)
    {
        return null;
    }

    private function hydrate(array $row)
    {
    }
}

function helper()
{
    return 1;
}
