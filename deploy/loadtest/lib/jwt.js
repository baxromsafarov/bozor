// HS256 JWT-минтер для нагрузочных сценариев. Гейтвей принимает access-токены,
// подписанные ключом JWT_SIGNING_KEY (см. pkg/shared/authx/signer.go): claims
// sub=user_id, roles, jti, iss, iat, exp. Issuer при разборе не проверяется
// (authx.Parse без WithIssuer), поэтому берём фиксированный "auth".
//
// Минтим токен прямо в k6 (k6/crypto HMAC-SHA256), чтобы не гонять OTP-поток
// Telegram под нагрузкой — авторизация как поток проверяется на стороне гейтвея
// (валидация подписи + проброс X-User-Id/X-User-Roles на каждый auth-запрос).
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';

function b64url(str) {
  return encoding.b64encode(str, 'rawurl');
}

// mintJWT выпускает HS256 access-токен для userID с ролями roles, живущий ttlSec.
export function mintJWT(signingKey, userID, roles, ttlSec) {
  const now = Math.floor(Date.now() / 1000);
  const header = { alg: 'HS256', typ: 'JWT' };
  const payload = {
    sub: userID,
    roles: roles,
    // jti детерминированно уникален на пользователя — под нагрузкой достаточно.
    jti: userID + '-' + now,
    iss: 'auth',
    iat: now,
    exp: now + (ttlSec || 3600),
  };
  const signingInput = b64url(JSON.stringify(header)) + '.' + b64url(JSON.stringify(payload));
  const sig = crypto.hmac('sha256', signingKey, signingInput, 'base64rawurl');
  return signingInput + '.' + sig;
}

// syntheticSeller строит стабильный uuid-подобный id продавца по индексу VU —
// чтобы разные VU писали от разных пользователей (реалистичнее для лимитов/квот).
export function syntheticSeller(n) {
  const hex = (1000000 + (n % 1000000)).toString().padStart(12, '0');
  return 'a5eddddd-0000-4000-8000-' + hex;
}
