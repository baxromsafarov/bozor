-- Token bucket rate limiter (атомарно в Redis).
-- KEYS[1] = ключ bucket'а
-- ARGV[1] = rate  — пополнение токенов в секунду
-- ARGV[2] = burst — ёмкость bucket'а
-- ARGV[3] = requested — сколько токенов запросить (обычно 1)
-- Возврат: { allowed(0|1), remaining(int), reset_ms(int) }
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local requested = tonumber(ARGV[3])

-- Время берём с сервера Redis, чтобы реплики gateway были согласованы.
local t = redis.call('TIME')
local now_ms = (tonumber(t[1]) * 1000) + math.floor(tonumber(t[2]) / 1000)

local bucket = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(bucket[1])
local ts = tonumber(bucket[2])
if tokens == nil then
  tokens = burst
  ts = now_ms
end

-- Пополнение с момента последнего обращения.
local elapsed = math.max(0, now_ms - ts) / 1000.0
tokens = math.min(burst, tokens + elapsed * rate)

local allowed = 0
if tokens >= requested then
  allowed = 1
  tokens = tokens - requested
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'ts', now_ms)
-- TTL = время полного пополнения: неактивный ключ сам исчезнет.
local full_ms = math.ceil((burst / rate) * 1000)
redis.call('PEXPIRE', KEYS[1], full_ms)

-- reset_ms — через сколько появится достаточно токенов (только при отказе).
local reset_ms = 0
if allowed == 0 then
  reset_ms = math.ceil(((requested - tokens) / rate) * 1000)
end

return { allowed, math.floor(tokens), reset_ms }
