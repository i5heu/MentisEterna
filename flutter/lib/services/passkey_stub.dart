/// Native platform passkey implementation using the `passkeys` package.
///
/// Supports iOS, Android, and macOS via the [PasskeyAuthenticator] API.
/// Falls back to [UnsupportedError] on Linux and Windows.
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:passkeys/authenticator.dart';
import 'package:passkeys/exceptions.dart';
import 'package:passkeys/types.dart';

final _authenticator = PasskeyAuthenticator();

// ---------------------------------------------------------------------------
// Login (authentication)
// ---------------------------------------------------------------------------

Future<Map<String, dynamic>?> platformGetCredential(
    Map<String, dynamic> options) async {
  print('[passkey:native] platformGetCredential() called');
  try {
    final pk = options['publicKey'] as Map<String, dynamic>;

    final result = await _authenticator.authenticate(
      AuthenticateRequestType(
        relyingPartyId: pk['rpId'] as String,
        challenge: _toBase64Url(pk['challenge'] as Uint8List),
        timeout: pk['timeout'] as int?,
        userVerification: pk['userVerification'] as String? ?? 'required',
        allowCredentials: (pk['allowCredentials'] as List<dynamic>?)
                ?.map((c) => CredentialType(
                      id: _toBase64Url(c['id'] as Uint8List),
                      type: c['type'] as String? ?? 'public-key',
                      transports: (c['transports'] as List<dynamic>?)
                              ?.map((t) => t.toString())
                              .toList() ??
                          [],
                    ))
                .toList() ??
            [],
        mediation: MediationType.Optional,
        preferImmediatelyAvailableCredentials: true,
      ),
    );

    print('[passkey:native] Authenticate success');
    // AuthenticateResponseType has flat fields (no .response wrapper).
    return {
      'id': result.id,
      'rawId': _fromBase64Url(result.rawId),
      'type': 'public-key',
      'response': {
        'clientDataJSON': _fromBase64Url(result.clientDataJSON),
        'authenticatorData': _fromBase64Url(result.authenticatorData),
        'signature': _fromBase64Url(result.signature),
        'userHandle': result.userHandle.isNotEmpty
            ? _fromBase64Url(result.userHandle)
            : null,
      },
    };
  } on PasskeyAuthCancelledException {
    print('[passkey:native] User cancelled.');
    return null;
  } on NoCredentialsAvailableException {
    print('[passkey:native] No credentials available.');
    return null;
  } catch (e, st) {
    print('[passkey:native] Authenticate failed: $e');
    print('[passkey:native] Stack: $st');
    rethrow;
  }
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

Future<Map<String, dynamic>?> platformCreateCredential(
    Map<String, dynamic> options) async {
  print('[passkey:native] platformCreateCredential() called');
  try {
    final pk = options['publicKey'] as Map<String, dynamic>;
    final user = pk['user'] as Map<String, dynamic>;
    final rp = pk['rp'] as Map<String, dynamic>;

    // Build AuthenticatorSelectionType if present.
    // Requires: requireResidentKey (bool), residentKey (String), userVerification (String).
    final sel = pk['authenticatorSelection'] as Map<String, dynamic>?;

    final result = await _authenticator.register(
      RegisterRequestType(
        relyingParty: RelyingPartyType(
          name: rp['name'] as String,
          id: rp['id'] as String,
        ),
        user: UserType(
          name: user['name'] as String,
          displayName: user['displayName'] as String,
          id: _toBase64Url(user['id'] as Uint8List),
        ),
        challenge: _toBase64Url(pk['challenge'] as Uint8List),
        timeout: pk['timeout'] as int?,
        attestation: pk['attestation'] as String? ?? 'none',
        authSelectionType: sel != null
            ? AuthenticatorSelectionType(
                authenticatorAttachment:
                    sel['authenticatorAttachment'] as String?,
                requireResidentKey: sel['requireResidentKey'] as bool? ?? true,
                residentKey: sel['residentKey'] as String? ?? 'required',
                userVerification:
                    sel['userVerification'] as String? ?? 'required',
              )
            : null,
        excludeCredentials: (pk['excludeCredentials'] as List<dynamic>?)
                ?.map((c) => CredentialType(
                      id: _toBase64Url(c['id'] as Uint8List),
                      type: c['type'] as String? ?? 'public-key',
                      transports: (c['transports'] as List<dynamic>?)
                              ?.map((t) => t.toString())
                              .toList() ??
                          [],
                    ))
                .toList() ??
            [],
      ),
    );

    print('[passkey:native] Register success');
    // RegisterResponseType has flat fields (no .response wrapper).
    return {
      'id': result.id,
      'rawId': _fromBase64Url(result.rawId),
      'type': 'public-key',
      'response': {
        'clientDataJSON': _fromBase64Url(result.clientDataJSON),
        'attestationObject': _fromBase64Url(result.attestationObject),
        'transports': result.transports.whereType<String>().toList(),
      },
    };
  } on PasskeyAuthCancelledException {
    print('[passkey:native] User cancelled.');
    return null;
  } on ExcludeCredentialsCanNotBeRegisteredException {
    print('[passkey:native] Exclude credentials match — already registered.');
    return null;
  } catch (e, st) {
    print('[passkey:native] Register failed: $e');
    print('[passkey:native] Stack: $st');
    rethrow;
  }
}

// ---------------------------------------------------------------------------
// Base64url ↔ bytes
// ---------------------------------------------------------------------------

String _toBase64Url(Uint8List bytes) {
  return base64UrlEncode(bytes);
}

Uint8List _fromBase64Url(String input) {
  return base64UrlDecode(input);
}

String base64UrlEncode(Uint8List bytes) {
  final b64 = base64Encode(bytes);
  return b64.replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', '');
}

Uint8List base64UrlDecode(String input) {
  String n = input.replaceAll('-', '+').replaceAll('_', '/');
  switch (n.length % 4) {
    case 2:
      n += '==';
    case 3:
      n += '=';
  }
  return Uint8List.fromList(base64Decode(n));
}
