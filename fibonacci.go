package main

import "fmt"

// FibonacciMemo calculates Fibonacci numbers using memoization
func FibonacciMemo(n int, memo map[int]int) int {
    if n <= 1 {
        return n
    }
    
    if val, exists := memo[n]; exists {
        return val
    }
    
    memo[n] = FibonacciMemo(n-1, memo) + FibonacciMemo(n-2, memo)
    return memo[n]
}

// Fibonacci wrapper function
func Fibonacci(n int) int {
    memo := make(map[int]int)
    return FibonacciMemo(n, memo)
}

func main() {
    fmt.Println("Fibonacci(10):", Fibonacci(10))
}
