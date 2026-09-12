package com.example.tvapp.nav

import androidx.compose.runtime.Composable
import androidx.navigation.NavHostController

@Composable
fun AppNavHost(navController: NavHostController) {
    NavHost(navController = navController, startDestination = "home") {
        composable(route = "home") {
            HomeScreen(
                onOpenDetails = { navController.navigate("details") }
            )
        }
        composable(
            route = "details/{itemId}",
            arguments = listOf(
                navArgument(name = "itemId") { type = NavType.StringType }
            )
        ) { backStackEntry ->
            DetailsScreen(itemId = backStackEntry.arguments?.getString("itemId"))
        }
        navigation(route = "settings", startDestination = "settingsRoot") {
            composable(route = "settingsRoot") { SettingsScreen() }
        }
    }
}
