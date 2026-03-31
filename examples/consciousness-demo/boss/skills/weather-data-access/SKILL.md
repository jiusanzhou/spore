---
name: weather-data-access
description: Access real-time weather data through APIs with proper fallback strategies
version: 0.2.0
category: core
origin: captured
source_task: 2e39a5a3
tools:
  - name: get-weather
    description: Get current weather conditions for a location
    command: 'curl -s "wttr.in/{{input}}?format=3"'
    timeout: 10s
  - name: get-weather-detail
    description: Get detailed weather forecast for a location
    command: 'curl -s "wttr.in/{{input}}?format=%l:+%c+%t+(feels+like+%f),+%w+wind,+%h+humidity"'
    timeout: 10s
  - name: get-weather-forecast
    description: Get 3-day weather forecast for a location
    command: 'curl -s "wttr.in/{{input}}?0"'
    timeout: 15s
created_at: "2026-03-30T17:07:17+08:00"
updated_at: "2026-03-31T09:50:00+08:00"
---

# Weather Data Access

## When to Use
- User asks for current weather conditions for a specific location
- User requests weather forecasts (short-term or extended)
- User needs weather data for trip planning or outdoor activities
- User asks about weather-related metrics (temperature, humidity, precipitation, wind)
- User wants to compare weather across multiple locations
- Weather data is needed as context for other recommendations or decisions

## Procedure
1. **Parse location request**
   - Extract specific location (city, coordinates, zip code, etc.)
   - Standardize location format for API compatibility
   - Handle ambiguous locations by asking for clarification

2. **Attempt primary weather API access**
   - Use reliable weather service API (OpenWeatherMap, WeatherAPI, etc.)
   - Include API key authentication if required
   - Request appropriate data scope (current, forecast, historical)

3. **Implement fallback strategies**
   - If primary API fails, try secondary weather service
   - Consider web scraping reputable weather sites as backup
   - Use cached weather data if recent (within 30-60 minutes)

4. **Process and validate data**
   - Check data completeness and reasonableness
   - Convert units as needed (Celsius/Fahrenheit, mph/km/h)
   - Extract relevant metrics for user's specific question

5. **Format response appropriately**
   - Present data in user-friendly format
   - Include relevant details without overwhelming
   - Add timestamps and data source attribution

## Pitfalls
- **API rate limits**: Exceeding request quotas leading to blocked access
- **Invalid locations**: APIs may not recognize location strings or return wrong areas
- **Stale data**: Cached weather information becomes outdated quickly
- **Unit confusion**: Mixing temperature scales or measurement systems
- **Network timeouts**: Weather services may be temporarily unavailable
- **Incomplete data**: Some locations may have limited weather station coverage
- **API key exposure**: Accidentally revealing authentication credentials
- **Over-reliance on single source**: No backup when primary service fails

## Verification
- Weather data includes current timestamp within reasonable time window
- Temperature and conditions seem plausible for location and season
- All requested metrics (temperature, humidity, etc.) are present
- Location matches user's request (verify city/region names)
- Units are clearly specified and consistent
- Forecast data includes appropriate time ranges if requested
- Error handling works when invalid locations are provided
- Fallback sources activate when primary API is unavailable
