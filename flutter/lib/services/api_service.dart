import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import '../models/models.dart';

/// HTTP client for the MentisEterna server API.
class ApiService {
  final String baseUrl;
  final http.Client _client;

  String? _token;

  ApiService({required this.baseUrl, http.Client? client})
      : _client = client ?? http.Client();

  /// The current auth token, or null if not logged in.
  String? get token => _token;

  /// Whether we have an auth token.
  bool get isAuthenticated => _token != null && _token!.isNotEmpty;

  // ---------------------------------------------------------------------------
  // Auth
  // ---------------------------------------------------------------------------

  /// Log in with username + password.
  Future<LoginResponse> login(String username, String password) async {
    final res = await _post('/login', body: {
      'username': username,
      'password': password,
    });
    final lr = LoginResponse.fromJson(_decodeBody(res));
    _token = lr.token;
    return lr;
  }

  /// Begin WebAuthn / passkey login — returns the PublicKeyCredentialRequestOptions
  /// JSON that the platform authenticator needs.
  Future<Map<String, dynamic>> beginPasskeyLogin() async {
    final res = await _get('/webauthn/login/begin');
    return _decodeBody(res) as Map<String, dynamic>;
  }

  /// Finish WebAuthn / passkey login by sending the signed assertion.
  /// [assertion] is the JSON-encodable assertion response from the platform.
  Future<LoginResponse> finishPasskeyLogin(
      Map<String, dynamic> assertion) async {
    final res = await _post('/webauthn/login/finish', body: assertion);
    final lr = LoginResponse.fromJson(_decodeBody(res));
    _token = lr.token;
    return lr;
  }

  /// Begin WebAuthn / passkey registration — returns the
  /// PublicKeyCredentialCreationOptions JSON.
  Future<Map<String, dynamic>> beginPasskeyRegistration() async {
    final res = await _get('/webauthn/register/begin');
    return _decodeBody(res) as Map<String, dynamic>;
  }

  /// Finish WebAuthn / passkey registration.
  Future<void> finishPasskeyRegistration(
      Map<String, dynamic> attestation) async {
    await _post('/webauthn/register/finish', body: attestation);
  }

  /// Set the token directly (e.g. after restoring from secure storage).
  void setToken(String? token) {
    _token = token;
  }

  // ---------------------------------------------------------------------------
  // Notes
  // ---------------------------------------------------------------------------

  /// List all notes.
  Future<List<NoteSummary>> listNotes() async {
    final res = await _get('/notes');
    final list = _decodeBody(res) as List<dynamic>;
    return list
        .map((e) => NoteSummary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Get a single note by [id].
  Future<NoteDetail> getNote(int id) async {
    final res = await _get('/notes/$id');
    return NoteDetail.fromJson(_decodeBody(res) as Map<String, dynamic>);
  }

  /// Search notes semantically.
  Future<List<SearchResult>> searchNotes(String query) async {
    final res = await _get('/notes/search?q=${Uri.encodeQueryComponent(query)}');
    final list = _decodeBody(res) as List<dynamic>;
    return list
        .map((e) => SearchResult.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Get children of a note.
  Future<List<NoteSummary>> getNoteChildren(int id) async {
    final res = await _get('/notes/$id/children');
    final list = _decodeBody(res) as List<dynamic>;
    return list
        .map((e) => NoteSummary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Get ancestor chain for a note.
  Future<List<NoteSummary>> getNoteAncestors(int id) async {
    final res = await _get('/notes/$id/ancestors');
    final list = _decodeBody(res) as List<dynamic>;
    return list
        .map((e) => NoteSummary.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Get note history.
  Future<List<Map<String, dynamic>>> getNoteHistory(int id) async {
    final res = await _get('/notes/$id/history');
    final list = _decodeBody(res) as List<dynamic>;
    return list.map((e) => e as Map<String, dynamic>).toList();
  }

  /// Create a new note.
  Future<NoteDetail> createNote({
    required String title,
    required String body,
    int? parentId,
    String type = 'standard',
    Map<String, dynamic>? customData,
    List<String> tags = const [],
  }) async {
    final res = await _post('/notes', body: {
      'title': title,
      'body': body,
      if (parentId != null) 'parent_id': parentId,
      'type': type,
      if (customData != null) 'custom_data': customData,
      'tags': tags,
    });
    return NoteDetail.fromJson(_decodeBody(res) as Map<String, dynamic>);
  }

  /// Update an existing note.
  Future<NoteDetail> updateNote({
    required int id,
    required String title,
    required String body,
    int? parentId,
    String type = 'standard',
    Map<String, dynamic>? customData,
    List<String> tags = const [],
  }) async {
    final res = await _put('/notes/$id', body: {
      'title': title,
      'body': body,
      if (parentId != null) 'parent_id': parentId,
      'type': type,
      if (customData != null) 'custom_data': customData,
      'tags': tags,
    });
    return NoteDetail.fromJson(_decodeBody(res) as Map<String, dynamic>);
  }

  /// Delete a note.
  Future<void> deleteNote(int id) async {
    await _delete('/notes/$id');
  }

  /// Pin or unpin a note.
  Future<NoteDetail> setNotePin(int id, bool pinned) async {
    final res = await _post('/notes/$id/pin', body: {'pinned': pinned});
    return NoteDetail.fromJson(_decodeBody(res) as Map<String, dynamic>);
  }

  /// Execute a plugin action (legacy).
  Future<Map<String, dynamic>> pluginAction(
      int noteId, String action, Map<String, dynamic>? params) async {
    final res = await _post('/notes/$noteId/action', body: {
      'action': action,
      if (params != null) 'params': params,
    });
    return _decodeBody(res) as Map<String, dynamic>;
  }

  /// Execute a plugin action (v2).
  Future<Map<String, dynamic>> pluginActionV2(
      int noteId, String actionId, Map<String, dynamic>? params) async {
    final res = await _post('/notes/$noteId/actions/$actionId', body: {
      if (params != null) 'params': params,
    });
    return _decodeBody(res) as Map<String, dynamic>;
  }

  /// List available note types.
  Future<List<NoteTypeManifest>> fetchNoteTypes() async {
    final res = await _get('/note-types');
    final list = _decodeBody(res) as List<dynamic>;
    return list
        .map((e) => NoteTypeManifest.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ---------------------------------------------------------------------------
  // Tags
  // ---------------------------------------------------------------------------

  /// Fetch tags, optionally filtered by query.
  Future<List<String>> fetchTags({String? query}) async {
    final path = query != null && query.isNotEmpty
        ? '/tags?q=${Uri.encodeQueryComponent(query)}'
        : '/tags';
    final res = await _get(path);
    final list = _decodeBody(res) as List<dynamic>;
    return list.map((e) => e.toString()).toList();
  }

  // ---------------------------------------------------------------------------
  // Files
  // ---------------------------------------------------------------------------

  /// Upload a file as an attachment.
  Future<Map<String, dynamic>> uploadAttachment(
      int noteId, String filename, Uint8List bytes, String mimeType) async {
    final uri = Uri.parse('$baseUrl/notes/$noteId/files');
    final request = http.MultipartRequest('POST', uri);
    _addAuth(request);
    request.files.add(http.MultipartFile.fromBytes(
      'file',
      bytes,
      filename: filename,
      contentType: http.MediaType.parse(mimeType),
    ));
    final streamed = await _client.send(request);
    final res = await http.Response.fromStream(streamed);
    _checkError(res);
    return _decodeBody(res) as Map<String, dynamic>;
  }

  /// Delete an attachment.
  Future<void> deleteAttachment(int noteId, int fileId) async {
    await _delete('/notes/$noteId/files/$fileId');
  }

  // ---------------------------------------------------------------------------
  // Internal helpers
  // ---------------------------------------------------------------------------

  Map<String, String> get _authHeaders => {
        'Content-Type': 'application/json',
        if (_token != null) 'Authorization': 'Bearer $_token',
      };

  void _addAuth(http.BaseRequest request) {
    if (_token != null) {
      request.headers['Authorization'] = 'Bearer $_token';
    }
  }

  Future<http.Response> _get(String path) async {
    final uri = Uri.parse('$baseUrl$path');
    final res = await _client.get(uri, headers: _authHeaders);
    _checkError(res);
    return res;
  }

  Future<http.Response> _post(String path,
      {Map<String, dynamic>? body}) async {
    final uri = Uri.parse('$baseUrl$path');
    final res = await _client.post(
      uri,
      headers: _authHeaders,
      body: body != null ? jsonEncode(body) : null,
    );
    _checkError(res);
    return res;
  }

  Future<http.Response> _put(String path,
      {Map<String, dynamic>? body}) async {
    final uri = Uri.parse('$baseUrl$path');
    final res = await _client.put(
      uri,
      headers: _authHeaders,
      body: body != null ? jsonEncode(body) : null,
    );
    _checkError(res);
    return res;
  }

  Future<http.Response> _delete(String path) async {
    final uri = Uri.parse('$baseUrl$path');
    final res = await _client.delete(uri, headers: _authHeaders);
    _checkError(res);
    return res;
  }

  void _checkError(http.Response res) {
    if (res.statusCode >= 200 && res.statusCode < 300) return;
    String message;
    try {
      message = _decodeBody(res).toString();
    } catch (_) {
      message = res.body;
    }
    throw ApiException(res.statusCode, message);
  }

  dynamic _decodeBody(http.Response res) {
    if (res.body.isEmpty) return null;
    return jsonDecode(res.body);
  }

  void dispose() {
    _client.close();
  }
}

/// Exception thrown by [ApiService] on non-2xx responses.
class ApiException implements Exception {
  final int statusCode;
  final String message;

  const ApiException(this.statusCode, this.message);

  @override
  String toString() => 'ApiException($statusCode): $message';
}
