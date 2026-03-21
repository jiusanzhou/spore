import requests


def get_weather(city, api_key):
    base_url = "http://api.openweathermap.org/data/2.5/weather"
    params = {
        "q": city,
        "appid": api_key,
        "units": "metric"  # Use "imperial" for Fahrenheit
    }
    response = requests.get(base_url, params=params)
    if response.status_code == 200:
        data = response.json()
        weather_desc = data["weather"][0]["description"]
        temperature = data["main"]["temp"]
        print(f"Current weather in {city}:

Temperature: {temperature}°C
Description: {weather_desc}")


Temperature: {temperature}°C
Description: {weather_desc}
Description: {weather_desc}")
    else:
        print("City not found or API error.")

if __name__ == "__main__":
    city = input("Enter the city name: ")
    api_key = "YOUR_API_KEY"  # Replace with your OpenWeatherMap API key
    get_weather(city, api_key)
