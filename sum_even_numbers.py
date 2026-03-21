def sum_even_numbers(numbers):
    """
    Takes a list of numbers and returns the sum of all even numbers.
    
    Args:
        numbers: A list of numbers (integers or floats)
    
    Returns:
        The sum of all even numbers in the list
    """
    return sum(num for num in numbers if num % 2 == 0)

if __name__ == "__main__":
    test_list = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
    result = sum_even_numbers(test_list)
    print(f"Sum of even numbers in {test_list}: {result}")
