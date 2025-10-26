#!/bin/bash

# Test Slack Connection Script
# This helps debug why events are not being received

echo "🔍 BeerBot Slack Connection Test"
echo "================================"

# Check if environment variables are set
echo ""
echo "📋 Environment Check:"
if [ -z "$BOT_TOKEN" ]; then
    echo "❌ BOT_TOKEN is not set"
else
    echo "✅ BOT_TOKEN is set (starts with: ${BOT_TOKEN:0:10}...)"
fi

if [ -z "$APP_TOKEN" ]; then
    echo "❌ APP_TOKEN is not set"
else
    echo "✅ APP_TOKEN is set (starts with: ${APP_TOKEN:0:10}...)"
fi

if [ -z "$CHANNEL" ]; then
    echo "❌ CHANNEL is not set"
else
    echo "✅ CHANNEL is set: $CHANNEL"
fi

# Test API connection
echo ""
echo "🔌 Testing Slack API Connection:"
if [ -n "$BOT_TOKEN" ]; then
    curl -s -H "Authorization: Bearer $BOT_TOKEN" \
         "https://slack.com/api/auth.test" | jq .
else
    echo "❌ Cannot test - BOT_TOKEN not set"
fi

# Test Socket Mode setup
echo ""
echo "🔌 Testing Socket Mode Token:"
if [ -n "$APP_TOKEN" ]; then
    curl -s -H "Authorization: Bearer $APP_TOKEN" \
         "https://slack.com/api/auth.test" | jq .
else
    echo "❌ Cannot test - APP_TOKEN not set"
fi

echo ""
echo "🚀 Start BeerBot with DEBUG logging:"
echo "LOG_LEVEL=debug ./beerbot-debug"