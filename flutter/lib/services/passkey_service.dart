import 'dart:convert';
import 'dart:typed_data';

import '../models/models.dart';
import 'api_service.dart';
import 'passkey_web.dart' if (dart.library.io) 'passkey_stub.dart';

/// Handles WebAuthn / Passkey credential operations.
///
/// On Flutter **web**, uses the browser's native
/// `navigator.credentials` API via `dart:js_interop`.
///
/// On native platforms (iOS, Android, macOS, Linux, Windows), the stubs
/// throw [UnsupportedError]. Use a native passkey package (e.g. `passkeys`
/// from pub.dev) on those platforms.
class PasskeyService {
  final ApiService _api;

  PasskeyService(this._api);

  // ---------------------------------------------------------------------------
  // Base64url helpers
  // ---------------------------------------------------------------------------

  static String base64UrlEncode(Uint8List bytes) {
    return base64Encode(bytes)
        .replaceAll('+', '-')
        .replaceAll('/', '_')
        .replaceAll('=', '');
  }

  static Uint8List base64UrlDecode(String input) {
    String normalized = input.replaceAll('-', '+').replaceAll('_', '/');
    switch (normalized.length % 4) {
      case 2:
        normalized += '==';
      case 3:
        normalized += '=';
    }
    return base64Decode(normalized);
  }

  // ---------------------------------------------------------------------------
  // Login
  // ---------------------------------------------------------------------------

  /// Attempt passkey login.  Returns `null` if the user cancelled.
  Future<LoginResponse?> loginWithPasskey() async {
    print('[passkey] Starting passkey login flow...');
    final options = await _api.beginPasskeyLogin();
    print(
        '[passkey] Got challenge from server. publicKey keys: ${(options["publicKey"] as Map?)?.keys?.toList() ?? options.keys.toList()}');
    final publicKey = options['publicKey'] as Map<String, dynamic>? ?? options;
    final prepared = _prepareCredentialRequestOptions(publicKey);
    print(
        '[passkey] Prepared credential request options. challenge type: ${prepared["publicKey"]["challenge"].runtimeType}');

    final credential = await _getCredential(prepared);
    if (credential == null) {
      print('[passkey] Credential is null — user likely cancelled.');
      return null;
    }
    print('[passkey] Credential obtained. Encoding assertion...');

    final assertion = _encodeAssertionResponse(credential);
    print('[passkey] Sending assertion to server (id: ${assertion["id"]})...');
    return _api.finishPasskeyLogin(assertion);
  }

  Map<String, dynamic> _prepareCredentialRequestOptions(
      Map<String, dynamic> publicKey) {
    final challengeBytes = base64UrlDecode(publicKey['challenge'] as String);

    List<Map<String, dynamic>>? allowCredentials;
    if (publicKey['allowCredentials'] != null) {
      allowCredentials = (publicKey['allowCredentials'] as List).map((c) {
        final cred = c as Map<String, dynamic>;
        return {
          ...cred,
          'id': base64UrlDecode(cred['id'] as String),
        };
      }).toList();
    }

    return {
      'publicKey': {
        ...publicKey,
        'challenge': challengeBytes,
        if (allowCredentials != null) 'allowCredentials': allowCredentials,
      },
    };
  }

  Map<String, dynamic> _encodeAssertionResponse(
      Map<String, dynamic> credential) {
    final response = credential['response'] as Map<String, dynamic>;
    return {
      'id': credential['id'] as String,
      'rawId': base64UrlEncode(credential['rawId'] as Uint8List),
      'type': credential['type'] as String,
      'response': {
        'clientDataJSON':
            base64UrlEncode(response['clientDataJSON'] as Uint8List),
        'authenticatorData':
            base64UrlEncode(response['authenticatorData'] as Uint8List),
        'signature': base64UrlEncode(response['signature'] as Uint8List),
        'userHandle': response['userHandle'] != null
            ? base64UrlEncode(response['userHandle'] as Uint8List)
            : null,
      },
    };
  }

  // ---------------------------------------------------------------------------
  // Registration
  // ---------------------------------------------------------------------------

  /// Register a new passkey (requires existing auth session).
  Future<bool> registerPasskey() async {
    final creationOptions = await _api.beginPasskeyRegistration();
    final publicKey = creationOptions['publicKey'] as Map<String, dynamic>? ??
        creationOptions;

    final prepared = _prepareCredentialCreationOptions(publicKey);

    try {
      final credential = await _createCredential(prepared);
      if (credential == null) return false;

      final attestation = _encodeAttestationResponse(credential);
      await _api.finishPasskeyRegistration(attestation);
      return true;
    } catch (e) {
      return false;
    }
  }

  Map<String, dynamic> _prepareCredentialCreationOptions(
      Map<String, dynamic> publicKey) {
    final challengeBytes = base64UrlDecode(publicKey['challenge'] as String);

    Map<String, dynamic> user = publicKey['user'] as Map<String, dynamic>;
    user = {...user, 'id': base64UrlDecode(user['id'] as String)};

    List<Map<String, dynamic>>? excludeCredentials;
    if (publicKey['excludeCredentials'] != null) {
      excludeCredentials = (publicKey['excludeCredentials'] as List).map((c) {
        final cred = c as Map<String, dynamic>;
        return {
          ...cred,
          'id': base64UrlDecode(cred['id'] as String),
        };
      }).toList();
    }

    return {
      'publicKey': {
        ...publicKey,
        'challenge': challengeBytes,
        'user': user,
        if (excludeCredentials != null)
          'excludeCredentials': excludeCredentials,
      },
    };
  }

  Map<String, dynamic> _encodeAttestationResponse(
      Map<String, dynamic> credential) {
    final response = credential['response'] as Map<String, dynamic>;
    return {
      'id': credential['id'] as String,
      'rawId': base64UrlEncode(credential['rawId'] as Uint8List),
      'type': credential['type'] as String,
      'response': {
        'clientDataJSON':
            base64UrlEncode(response['clientDataJSON'] as Uint8List),
        'attestationObject':
            base64UrlEncode(response['attestationObject'] as Uint8List),
        'transports': (response['transports'] as List<dynamic>?)
                ?.map((e) => e.toString())
                .toList() ??
            [],
      },
    };
  }

  // ---------------------------------------------------------------------------
  // Platform dispatch
  // ---------------------------------------------------------------------------

  /// Delegates to the web implementation or throws on native.
  Future<Map<String, dynamic>?> _getCredential(
      Map<String, dynamic> options) async {
    return platformGetCredential(options);
  }

  /// Delegates to the web implementation or throws on native.
  Future<Map<String, dynamic>?> _createCredential(
      Map<String, dynamic> options) async {
    return platformCreateCredential(options);
  }
}
