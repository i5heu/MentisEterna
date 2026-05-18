/// Native platform stub — passkey is not available outside the browser.
///
/// This file is compiled on all non-web targets (iOS, Android, macOS,
/// Linux, Windows). Use a native passkey package (e.g. `passkeys` from
/// pub.dev) on these platforms.
library;

import 'dart:typed_data';

/// Always throws [UnsupportedError] on native platforms.
Future<Map<String, dynamic>?> platformGetCredential(
    Map<String, dynamic> options) async {
  throw UnsupportedError(
    'Passkey authentication is not available on this platform.\n'
    'On mobile (iOS/Android), add the `passkeys` package from pub.dev.\n'
    'On desktop, use the web version or password login.',
  );
}

/// Always throws [UnsupportedError] on native platforms.
Future<Map<String, dynamic>?> platformCreateCredential(
    Map<String, dynamic> options) async {
  throw UnsupportedError(
    'Passkey registration is not available on this platform.\n'
    'On mobile (iOS/Android), add the `passkeys` package from pub.dev.\n'
    'On desktop, use the web version or password login.',
  );
}
