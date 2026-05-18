/// Web implementation of the platform credential API.
///
/// Uses `dart:js` to call the browser's native
/// `navigator.credentials.get()` and `navigator.credentials.create()`.
///
/// This file is only compiled on web targets — see the conditional import
/// in `passkey_service.dart`.
// ignore_for_file: deprecated_member_use
library;

import 'dart:async';
import 'dart:js' as js;
import 'dart:typed_data';

/// Calls `navigator.credentials.get(options)` in the browser.
///
/// Returns the credential as a Dart map, or `null` if the user cancelled.
Future<Map<String, dynamic>?> platformGetCredential(
    Map<String, dynamic> options) async {
  print('[passkey:web] platformGetCredential() called');
  try {
    final cred = js.context['navigator']['credentials'];
    print('[passkey:web] navigator.credentials found: ${cred != null}');
    final jsOpts = _mapToJs(options);
    print('[passkey:web] Calling navigator.credentials.get()...');
    final promise = cred.callMethod('get', [jsOpts]);
    final result = await _promiseToFuture(promise);
    if (result == null) {
      print(
          '[passkey:web] navigator.credentials.get() returned null (user cancelled?)');
      return null;
    }
    print('[passkey:web] Credential received, converting to Dart map...');
    final map = _credentialToMap(result);
    print(
        '[passkey:web] Conversion done. id=${map["id"]}, type=${map["type"]}');
    return map;
  } catch (e, st) {
    print('[passkey:web] navigator.credentials.get() threw: $e');
    print('[passkey:web] Stack: $st');
    final msg = e.toString().toLowerCase();
    if (msg.contains('abort') || msg.contains('notallowed')) return null;
    rethrow;
  }
}

/// Calls `navigator.credentials.create(options)` in the browser.
Future<Map<String, dynamic>?> platformCreateCredential(
    Map<String, dynamic> options) async {
  print('[passkey:web] platformCreateCredential() called');
  try {
    final cred = js.context['navigator']['credentials'];
    print('[passkey:web] navigator.credentials found: ${cred != null}');
    final jsOpts = _mapToJs(options);
    print('[passkey:web] Calling navigator.credentials.create()...');
    final promise = cred.callMethod('create', [jsOpts]);
    final result = await _promiseToFuture(promise);
    if (result == null) {
      print(
          '[passkey:web] navigator.credentials.create() returned null (user cancelled?)');
      return null;
    }
    print('[passkey:web] Credential created, converting to Dart map...');
    final map = _credentialToMap(result);
    print(
        '[passkey:web] Conversion done. id=${map["id"]}, type=${map["type"]}');
    return map;
  } catch (e, st) {
    print('[passkey:web] navigator.credentials.create() threw: $e');
    print('[passkey:web] Stack: $st');
    if (e.toString().toLowerCase().contains('abort')) return null;
    rethrow;
  }
}

// ---------------------------------------------------------------------------
// Deep conversion: Dart map → JS object with ArrayBuffer for byte arrays
// ---------------------------------------------------------------------------

/// Recursively converts a Dart map to a JS object, replacing every
/// [Uint8List] with a proper JS `ArrayBuffer`.
dynamic _mapToJs(Map<String, dynamic> map) {
  final obj = js.JsObject(js.context['Object']);
  for (final entry in map.entries) {
    obj[entry.key] = _valueToJs(entry.value);
  }
  return obj;
}

dynamic _listToJs(List list) {
  final arr = js.JsObject(js.context['Array']);
  for (int i = 0; i < list.length; i++) {
    // Use callMethod('push', ...) to avoid direct index assignment issues
    // Actually, JS Array supports index assignment:
    arr[i] = _valueToJs(list[i]);
  }
  return arr;
}

dynamic _valueToJs(dynamic value) {
  if (value == null) return null;
  if (value is Uint8List) {
    // Convert Uint8List → ArrayBuffer (what WebAuthn expects)
    final jsBytes = js.JsObject(js.context['Uint8Array'], [value.length]);
    for (int i = 0; i < value.length; i++) {
      jsBytes[i] = value[i];
    }
    // Return the underlying ArrayBuffer
    return jsBytes['buffer'];
  }
  if (value is Map<String, dynamic>) {
    return _mapToJs(value);
  }
  if (value is List) {
    return _listToJs(value);
  }
  // Primitives: String, int, double, bool — jsify handles these fine
  return value;
}

// ---------------------------------------------------------------------------
// Promise → Future bridge
// ---------------------------------------------------------------------------

Future<dynamic> _promiseToFuture(dynamic promise) {
  final c = Completer<dynamic>();
  final onOk = js.allowInterop((dynamic v) {
    if (!c.isCompleted) c.complete(v);
  });
  final onErr = js.allowInterop((dynamic e) {
    if (!c.isCompleted) c.completeError(e);
  });
  final p = js.context['Promise'].callMethod('resolve', [promise]);
  p.callMethod('then', [onOk, onErr]);
  return c.future;
}

// ---------------------------------------------------------------------------
// Credential → Dart map
// ---------------------------------------------------------------------------

Map<String, dynamic> _credentialToMap(dynamic cred) {
  final jsCred = cred as js.JsObject;
  return {
    'id': _str(jsCred['id']),
    'rawId': _arrayBufferToBytes(jsCred['rawId']),
    'type': _str(jsCred['type']),
    'response': _responseToMap(jsCred['response'] as js.JsObject),
  };
}

Map<String, dynamic> _responseToMap(js.JsObject resp) {
  final m = <String, dynamic>{
    'clientDataJSON': _arrayBufferToBytes(resp['clientDataJSON']),
    'authenticatorData': _arrayBufferToBytes(resp['authenticatorData']),
    'signature': _arrayBufferToBytes(resp['signature']),
  };

  final uh = resp['userHandle'];
  if (uh != null) {
    m['userHandle'] = _arrayBufferToBytes(uh);
  }

  final ao = resp['attestationObject'];
  if (ao != null) {
    m['attestationObject'] = _arrayBufferToBytes(ao);
  }

  try {
    m['transports'] = _jsListToDart(resp.callMethod('getTransports'));
  } catch (_) {
    m['transports'] = [];
  }

  return m;
}

// ---------------------------------------------------------------------------
// Low-level helpers
// ---------------------------------------------------------------------------

Uint8List _arrayBufferToBytes(dynamic ab) {
  if (ab == null) return Uint8List(0);

  final Uint8Array = js.context['Uint8Array'];
  final view = js.JsObject(Uint8Array, [ab]);
  final len = view['length'] as int;
  final result = Uint8List(len);
  for (int i = 0; i < len; i++) {
    result[i] = view[i] as int;
  }
  return result;
}

List<String> _jsListToDart(dynamic arr) {
  if (arr == null) return [];
  final a = arr as js.JsObject;
  final len = a['length'] as int;
  return [for (int i = 0; i < len; i++) _str(a[i])];
}

String _str(dynamic val) {
  if (val == null) return '';
  return val.toString();
}
