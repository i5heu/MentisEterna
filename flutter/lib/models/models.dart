/// Data models matching the MentisEterna server API responses.

class NoteSummary {
  final int id;
  final String title;
  final int? parentId;
  final String type;
  final bool pinned;
  final String body;
  final String createdAt;
  final String updatedAt;

  const NoteSummary({
    required this.id,
    required this.title,
    this.parentId,
    required this.type,
    required this.pinned,
    required this.body,
    required this.createdAt,
    required this.updatedAt,
  });

  factory NoteSummary.fromJson(Map<String, dynamic> json) {
    return NoteSummary(
      id: json['id'] as int,
      title: json['title'] as String? ?? '',
      parentId: json['parent_id'] as int?,
      type: json['type'] as String? ?? 'standard',
      pinned: json['pinned'] as bool? ?? false,
      body: json['body'] as String? ?? '',
      createdAt: json['created_at'] as String? ?? '',
      updatedAt: json['updated_at'] as String? ?? '',
    );
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'title': title,
        'parent_id': parentId,
        'type': type,
        'pinned': pinned,
        'body': body,
        'created_at': createdAt,
        'updated_at': updatedAt,
      };
}

class NoteFile {
  final int id;
  final String filename;
  final String mimeType;
  final int sizeBytes;
  final String url;
  final bool isImage;
  final bool isAudio;

  const NoteFile({
    required this.id,
    required this.filename,
    required this.mimeType,
    required this.sizeBytes,
    required this.url,
    required this.isImage,
    required this.isAudio,
  });

  factory NoteFile.fromJson(Map<String, dynamic> json) {
    return NoteFile(
      id: json['id'] as int,
      filename: json['filename'] as String? ?? '',
      mimeType: json['mime_type'] as String? ?? '',
      sizeBytes: json['size_bytes'] as int? ?? 0,
      url: json['url'] as String? ?? '',
      isImage: json['is_image'] as bool? ?? false,
      isAudio: json['is_audio'] as bool? ?? false,
    );
  }
}

class PluginDetail {
  final String type;
  final Map<String, dynamic>? config;
  final Map<String, dynamic>? view;

  const PluginDetail({
    required this.type,
    this.config,
    this.view,
  });

  factory PluginDetail.fromJson(Map<String, dynamic> json) {
    return PluginDetail(
      type: json['type'] as String? ?? '',
      config: json['config'] as Map<String, dynamic>?,
      view: json['view'] as Map<String, dynamic>?,
    );
  }
}

class NoteDetail {
  final int id;
  final String title;
  final int? parentId;
  final String type;
  final bool pinned;
  final String body;
  final String createdAt;
  final String updatedAt;
  final PluginDetail? plugin;
  final List<String> tags;
  final List<NoteFile> attachments;

  const NoteDetail({
    required this.id,
    required this.title,
    this.parentId,
    required this.type,
    required this.pinned,
    required this.body,
    required this.createdAt,
    required this.updatedAt,
    this.plugin,
    required this.tags,
    required this.attachments,
  });

  factory NoteDetail.fromJson(Map<String, dynamic> json) {
    return NoteDetail(
      id: json['id'] as int,
      title: json['title'] as String? ?? '',
      parentId: json['parent_id'] as int?,
      type: json['type'] as String? ?? 'standard',
      pinned: json['pinned'] as bool? ?? false,
      body: json['body'] as String? ?? '',
      createdAt: json['created_at'] as String? ?? '',
      updatedAt: json['updated_at'] as String? ?? '',
      plugin: json['plugin'] != null
          ? PluginDetail.fromJson(json['plugin'] as Map<String, dynamic>)
          : null,
      tags: (json['tags'] as List<dynamic>?)
              ?.map((e) => e.toString())
              .toList() ??
          [],
      attachments: (json['attachments'] as List<dynamic>?)
              ?.map((e) => NoteFile.fromJson(e as Map<String, dynamic>))
              .toList() ??
          [],
    );
  }
}

class NoteTypeManifest {
  final String id;
  final String label;
  final String description;
  final String category;
  final int sortOrder;
  final EditorMeta editor;
  final ViewerMeta viewer;
  final bool hasConfig;
  final bool hasView;
  final bool hasActions;
  final List<ActionMeta> actions;

  const NoteTypeManifest({
    required this.id,
    required this.label,
    required this.description,
    required this.category,
    required this.sortOrder,
    required this.editor,
    required this.viewer,
    required this.hasConfig,
    required this.hasView,
    required this.hasActions,
    required this.actions,
  });

  factory NoteTypeManifest.fromJson(Map<String, dynamic> json) {
    return NoteTypeManifest(
      id: json['id'] as String? ?? '',
      label: json['label'] as String? ?? '',
      description: json['description'] as String? ?? '',
      category: json['category'] as String? ?? '',
      sortOrder: json['sort_order'] as int? ?? 0,
      editor: EditorMeta.fromJson(json['editor'] as Map<String, dynamic>? ?? {}),
      viewer: ViewerMeta.fromJson(json['viewer'] as Map<String, dynamic>? ?? {}),
      hasConfig: json['has_config'] as bool? ?? false,
      hasView: json['has_view'] as bool? ?? false,
      hasActions: json['has_actions'] as bool? ?? false,
      actions: (json['actions'] as List<dynamic>?)
              ?.map((e) => ActionMeta.fromJson(e as Map<String, dynamic>))
              .toList() ??
          [],
    );
  }
}

class EditorMeta {
  final String mode;
  final Map<String, dynamic>? schema;

  const EditorMeta({required this.mode, this.schema});

  factory EditorMeta.fromJson(Map<String, dynamic> json) {
    return EditorMeta(
      mode: json['mode'] as String? ?? 'none',
      schema: json['schema'] as Map<String, dynamic>?,
    );
  }
}

class ViewerMeta {
  final String mode;

  const ViewerMeta({required this.mode});

  factory ViewerMeta.fromJson(Map<String, dynamic> json) {
    return ViewerMeta(mode: json['mode'] as String? ?? 'none');
  }
}

class ActionMeta {
  final String id;
  final String label;

  const ActionMeta({required this.id, required this.label});

  factory ActionMeta.fromJson(Map<String, dynamic> json) {
    return ActionMeta(
      id: json['id'] as String? ?? '',
      label: json['label'] as String? ?? '',
    );
  }
}

class LoginResponse {
  final String token;
  final String expiresAt;

  const LoginResponse({required this.token, required this.expiresAt});

  factory LoginResponse.fromJson(Map<String, dynamic> json) {
    return LoginResponse(
      token: json['token'] as String? ?? '',
      expiresAt: json['expires_at'] as String? ?? '',
    );
  }
}

class SearchResult extends NoteSummary {
  final double distance;

  SearchResult({
    required super.id,
    required super.title,
    super.parentId,
    required super.type,
    required super.pinned,
    required super.body,
    required super.createdAt,
    required super.updatedAt,
    required this.distance,
  });

  factory SearchResult.fromJson(Map<String, dynamic> json) {
    return SearchResult(
      id: json['id'] as int,
      title: json['title'] as String? ?? '',
      parentId: json['parent_id'] as int?,
      type: json['type'] as String? ?? 'standard',
      pinned: json['pinned'] as bool? ?? false,
      body: json['body'] as String? ?? '',
      createdAt: json['created_at'] as String? ?? '',
      updatedAt: json['updated_at'] as String? ?? '',
      distance: (json['distance'] as num?)?.toDouble() ?? 0.0,
    );
  }
}
