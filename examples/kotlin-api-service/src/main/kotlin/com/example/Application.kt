package com.example

import com.example.controller.ApiController

fun main() {
    val controller = ApiController()
    controller.handleGetUser("user-1")
    controller.handleCreateOrder("user-1", "item-42")
}
