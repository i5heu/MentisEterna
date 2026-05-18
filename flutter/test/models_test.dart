import 'package:flutter_test/flutter_test.dart';

import 'package:mentiseterna/models/models.dart';
import 'package:mentiseterna/services/api_service.dart';
import 'package:mentiseterna/services/passkey_service.dart';

void main() {
  group('NoteSummary', () {
    test('fromJson parses a full object', () {
      final json = {
        'id': 1,
        'title': 'Test Note',
        'parent_id': null,
        'type': 'standard',
        'pinned': false,
        'body': 'Hello world',
        'created_at': '2024-01-01T00:00:00Z',
        'updated_at': '2024-01-02T00:00:00Z',
      };

      final note = NoteSummary.fromJson(json);

      expect(note.id, 1);
      expect(note.title, 'Test Note');
      expect(note.parentId, isNull);
      expect(note.type, 'standard');
      expect(note.pinned, false);
      expect(note.body, 'Hello world');
    });

    test('fromJson handles missing fields gracefully', () {
      final json = <String, dynamic>{'id': 42};

      final note = NoteSummary.fromJson(json);

      expect(note.id, 42);
      expect(note.title, '');
      expect(note.body, '');
      expect(note.type, 'standard');
      expect(note.pinned, false);
    });
  });

  group('NoteDetail', () {
    test('fromJson parses with plugin, tags, and attachments', () {
      final json = {
        'id': 1,
        'title': 'Recipe',
        'parent_id': null,
        'type': 'recipe',
        'pinned': true,
        'body': 'Pancakes',
        'created_at': '2024-01-01T00:00:00Z',
        'updated_at': '2024-01-02T00:00:00Z',
        'plugin': {
          'type': 'recipe',
          'config': {'servings': 4},
          'view': {'prep_time': '10 min'},
        },
        'tags': ['breakfast', 'easy'],
        'attachments': [
          {
            'id': 101,
            'filename': 'pancake.jpg',
            'mime_type': 'image/jpeg',
            'size_bytes': 2048,
            'url': '/file/1/101',
            'is_image': true,
            'is_audio': false,
          },
        ],
      };

      final note = NoteDetail.fromJson(json);

      expect(note.id, 1);
      expect(note.pinned, true);
      expect(note.tags, ['breakfast', 'easy']);
      expect(note.attachments.length, 1);
      expect(note.attachments[0].filename, 'pancake.jpg');
      expect(note.attachments[0].isImage, true);
      expect(note.plugin?.type, 'recipe');
      expect(note.plugin?.config, {'servings': 4});
    });
  });

  group('LoginResponse', () {
    test('fromJson parses credentials', () {
      final json = {
        'token': 'abc123',
        'expires_at': '2024-12-31T23:59:59Z',
      };

      final response = LoginResponse.fromJson(json);

      expect(response.token, 'abc123');
      expect(response.expiresAt, '2024-12-31T23:59:59Z');
    });
  });

  group('PasskeyService base64url', () {
    test('base64UrlEncode round-trips', () {
      final original = 'hello world';
      final encoded = PasskeyService.base64UrlEncode(original.codeUnits);
      // Should not contain + / =
      expect(encoded.contains('+'), false);
      expect(encoded.contains('/'), false);
      expect(encoded.contains('='), false);

      final decoded = PasskeyService.base64UrlDecode(encoded);
      expect(String.fromCharCodes(decoded), 'hello world');
    });

    test('base64UrlDecode handles padded input', () {
      // "a" in standard base64 is "YQ==", in base64url it's "YQ"
      final decoded = PasskeyService.base64UrlDecode('YQ');
      expect(String.fromCharCodes(decoded), 'a');
    });
  });
}
