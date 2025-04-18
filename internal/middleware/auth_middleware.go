package middleware

import (
	"expo-open-ota/internal/auth"
	"expo-open-ota/internal/helpers"
	"expo-open-ota/internal/services"
	"fmt"
	"net/http"
)

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		useExpoAuth := r.Header.Get("Use-Expo-Auth")
		if useExpoAuth == "true" {
			expoAuth := helpers.GetExpoAuth(r)
			fmt.Println(expoAuth)
			_, err := services.ValidateExpoAuth(expoAuth)
			if err != nil {
				fmt.Println("lel", err)
				http.Error(w, "Invalid Expo auth", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		bearerToken, err := helpers.GetBearerToken(r)
		if err != nil {
			http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "No Authorization header provided", http.StatusUnauthorized)
			return
		}
		authService := auth.NewAuth()
		_, err = authService.ValidateToken(bearerToken)
		if err != nil {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)

	})
}
