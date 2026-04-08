import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:google_fonts/google_fonts.dart';

import 'package:flutter_muxpod/l10n/app_localizations.dart';

import '../../models/action_bar_config.dart';
import '../../models/action_bar_presets.dart';
import '../../providers/action_bar_provider.dart';
import '../../theme/design_colors.dart';

/// Settings screen for customizing action bar groups and buttons.
///
/// Allows reordering groups via drag handles, editing group contents,
/// adding/removing groups, and resetting to profile defaults.
class ActionBarSettingsScreen extends ConsumerStatefulWidget {
  const ActionBarSettingsScreen({super.key});

  @override
  ConsumerState<ActionBarSettingsScreen> createState() =>
      _ActionBarSettingsScreenState();
}

class _ActionBarSettingsScreenState
    extends ConsumerState<ActionBarSettingsScreen> {
  @override
  Widget build(BuildContext context) {
    final state = ref.watch(actionBarProvider);
    final profile = state.activeProfile;
    final groups = profile.groups;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          AppLocalizations.of(context)!.customizeToolbar,
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          PopupMenuButton<String>(
            onSelected: (value) {
              if (value == 'reset') {
                _confirmReset(context);
              }
            },
            itemBuilder: (context) => [
              PopupMenuItem(
                value: 'reset',
                child: Text(AppLocalizations.of(context)!.resetToDefault),
              ),
            ],
          ),
        ],
      ),
      body: Column(
        children: [
          // Profile name header
          Padding(
            padding: const EdgeInsets.all(16),
            child: Row(
              children: [
                Icon(Icons.view_column,
                    size: 20,
                    color: isDark
                        ? DesignColors.textSecondary
                        : DesignColors.textSecondaryLight),
                const SizedBox(width: 8),
                Text(
                  AppLocalizations.of(context)!.profileLabel(profile.name),
                  style: TextStyle(
                    fontSize: 14,
                    color: isDark
                        ? DesignColors.textSecondary
                        : DesignColors.textSecondaryLight,
                  ),
                ),
              ],
            ),
          ),
          // Reorderable group list
          Expanded(
            child: ReorderableListView.builder(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              itemCount: groups.length,
              onReorder: (oldIndex, newIndex) {
                ref
                    .read(actionBarProvider.notifier)
                    .reorderGroups(oldIndex, newIndex);
              },
              itemBuilder: (context, index) {
                final group = groups[index];
                return _GroupTile(
                  key: ValueKey(group.id),
                  group: group,
                  isDark: isDark,
                  onEdit: () => _editGroup(context, group),
                  onDelete: groups.length > 1
                      ? () => _confirmDeleteGroup(context, group)
                      : null,
                );
              },
            ),
          ),
          // Add group button
          SafeArea(
            top: false,
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: SizedBox(
                width: double.infinity,
                child: OutlinedButton.icon(
                  onPressed: () => _addGroup(context),
                  icon: const Icon(Icons.add),
                  label: Text(AppLocalizations.of(context)!.addGroup),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: colorScheme.primary,
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  void _confirmReset(BuildContext context) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(AppLocalizations.of(context)!.resetToDefault),
        content: Text(AppLocalizations.of(context)!.resetToDefaultDesc),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: Text(AppLocalizations.of(context)!.buttonCancel),
          ),
          FilledButton(
            onPressed: () {
              final state = ref.read(actionBarProvider);
              ref
                  .read(actionBarProvider.notifier)
                  .resetProfileToDefault(state.activeProfileId);
              Navigator.pop(ctx);
            },
            child: Text(AppLocalizations.of(context)!.reset),
          ),
        ],
      ),
    );
  }

  void _confirmDeleteGroup(BuildContext context, ActionBarGroup group) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(AppLocalizations.of(context)!.deleteGroup),
        content: Text(AppLocalizations.of(context)!.deleteGroupContent(group.name)),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: Text(AppLocalizations.of(context)!.buttonCancel),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.red),
            onPressed: () {
              ref.read(actionBarProvider.notifier).deleteGroup(group.id);
              Navigator.pop(ctx);
            },
            child: Text(AppLocalizations.of(context)!.buttonDelete),
          ),
        ],
      ),
    );
  }

  void _addGroup(BuildContext context) {
    final nameController = TextEditingController();
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(AppLocalizations.of(context)!.newGroup),
        content: TextField(
          controller: nameController,
          autofocus: true,
          decoration: InputDecoration(
            labelText: AppLocalizations.of(context)!.groupName,
            border: OutlineInputBorder(),
            isDense: true,
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: Text(AppLocalizations.of(context)!.buttonCancel),
          ),
          FilledButton(
            onPressed: () {
              final name = nameController.text.trim();
              if (name.isNotEmpty) {
                final newGroup = ActionBarGroup(
                  id: 'custom_${DateTime.now().millisecondsSinceEpoch}',
                  name: name,
                  buttons: const [],
                );
                ref.read(actionBarProvider.notifier).addGroup(newGroup);
                Navigator.pop(ctx);
              }
            },
            child: Text(AppLocalizations.of(context)!.buttonCreate),
          ),
        ],
      ),
    );
  }

  void _editGroup(BuildContext context, ActionBarGroup group) {
    Navigator.push(
      context,
      MaterialPageRoute(
        builder: (context) => _GroupEditorScreen(group: group),
      ),
    );
  }
}

class _GroupTile extends StatelessWidget {
  final ActionBarGroup group;
  final bool isDark;
  final VoidCallback onEdit;
  final VoidCallback? onDelete;

  const _GroupTile({
    super.key,
    required this.group,
    required this.isDark,
    required this.onEdit,
    this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    return Card(
      margin: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      child: ListTile(
        leading: const Icon(Icons.drag_handle),
        title: Text(
          group.name,
          style: const TextStyle(fontWeight: FontWeight.w600),
        ),
        subtitle: Text(
          group.buttons.map((b) => b.label).join(' | '),
          style: TextStyle(
            fontSize: 12,
            fontFamily: 'monospace',
            color: isDark
                ? DesignColors.textSecondary
                : DesignColors.textSecondaryLight,
          ),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            IconButton(
              icon: const Icon(Icons.edit, size: 20),
              onPressed: onEdit,
            ),
            if (onDelete != null)
              IconButton(
                icon: const Icon(Icons.delete_outline, size: 20),
                onPressed: onDelete,
              ),
          ],
        ),
      ),
    );
  }
}

/// Screen for editing buttons within a single group.
class _GroupEditorScreen extends ConsumerStatefulWidget {
  final ActionBarGroup group;

  const _GroupEditorScreen({required this.group});

  @override
  ConsumerState<_GroupEditorScreen> createState() => _GroupEditorScreenState();
}

class _GroupEditorScreenState extends ConsumerState<_GroupEditorScreen> {
  late TextEditingController _nameController;
  late List<ActionBarButton> _buttons;

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(text: widget.group.name);
    _buttons = List.from(widget.group.buttons);
  }

  @override
  void dispose() {
    _nameController.dispose();
    super.dispose();
  }

  void _save() {
    final name = _nameController.text.trim();
    if (name.isEmpty) return;

    final updated = ActionBarGroup(
      id: widget.group.id,
      name: name,
      buttons: _buttons,
    );
    ref.read(actionBarProvider.notifier).updateGroup(updated);
    Navigator.pop(context);
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final colorScheme = Theme.of(context).colorScheme;

    return Scaffold(
      appBar: AppBar(
        title: Text(
          AppLocalizations.of(context)!.editGroup,
          style: GoogleFonts.spaceGrotesk(fontWeight: FontWeight.w700),
        ),
        actions: [
          TextButton(
            onPressed: _save,
            child: Text(AppLocalizations.of(context)!.buttonSave),
          ),
        ],
      ),
      body: Column(
        children: [
          // Group name
          Padding(
            padding: const EdgeInsets.all(16),
            child: TextField(
              controller: _nameController,
              decoration: InputDecoration(
                labelText: AppLocalizations.of(context)!.groupName,
                border: OutlineInputBorder(),
                isDense: true,
              ),
            ),
          ),
          const Divider(),
          // Buttons list
          Expanded(
            child: ReorderableListView.builder(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              itemCount: _buttons.length,
              onReorder: (oldIndex, newIndex) {
                setState(() {
                  if (newIndex > oldIndex) newIndex--;
                  final item = _buttons.removeAt(oldIndex);
                  _buttons.insert(newIndex, item);
                });
              },
              itemBuilder: (context, index) {
                final button = _buttons[index];
                return Card(
                  key: ValueKey(button.id),
                  margin:
                      const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                  child: ListTile(
                    leading: const Icon(Icons.drag_handle),
                    title: Text(
                      button.label,
                      style: const TextStyle(fontWeight: FontWeight.w600),
                    ),
                    subtitle: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        if (button.displayDescription.isNotEmpty)
                          Text(
                            button.displayDescription,
                            style: TextStyle(
                              fontSize: 12,
                              color: isDark
                                  ? DesignColors.textSecondary
                                  : DesignColors.textSecondaryLight,
                            ),
                          ),
                        Text(
                          '${_buttonTypeLabel(button.type)} = ${button.value}'
                          '${button.longPressValue != null ? '  (long: ${button.longPressValue})' : ''}',
                          style: TextStyle(
                            fontSize: 11,
                            color: isDark
                                ? DesignColors.textMuted
                                : DesignColors.textMutedLight,
                          ),
                        ),
                      ],
                    ),
                    trailing: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        IconButton(
                          icon: const Icon(Icons.edit, size: 18),
                          onPressed: () =>
                              _showEditButtonDialog(context, index),
                          visualDensity: VisualDensity.compact,
                        ),
                        IconButton(
                          icon: const Icon(Icons.delete_outline, size: 18),
                          onPressed: () {
                            setState(() => _buttons.removeAt(index));
                          },
                          visualDensity: VisualDensity.compact,
                        ),
                      ],
                    ),
                  ),
                );
              },
            ),
          ),
          // Add button
          SafeArea(
            top: false,
            child: Padding(
              padding: const EdgeInsets.all(16),
              child: SizedBox(
                width: double.infinity,
                child: OutlinedButton.icon(
                  onPressed: () => _showAddButtonDialog(context),
                  icon: const Icon(Icons.add),
                  label: Text(AppLocalizations.of(context)!.addButton),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: colorScheme.primary,
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }

  static String _buttonTypeLabel(ActionBarButtonType type) {
    return switch (type) {
      ActionBarButtonType.specialKey => 'Key',
      ActionBarButtonType.literal => 'Literal',
      ActionBarButtonType.ctrlCombo => 'Ctrl',
      ActionBarButtonType.altCombo => 'Alt',
      ActionBarButtonType.shiftCombo => 'Shift',
      ActionBarButtonType.modifier => 'Modifier',
      ActionBarButtonType.action => 'Action',
      ActionBarButtonType.confirm => 'Confirm',
    };
  }

  void _showAddButtonDialog(BuildContext context) {
    _showButtonCatalogPicker(context, onPick: (button) {
      setState(() => _buttons.add(button));
    });
  }

  void _showEditButtonDialog(BuildContext context, int index) {
    final existing = _buttons[index];
    _showManualButtonDialog(
      context,
      title: AppLocalizations.of(context)!.editButton,
      initial: existing,
      onSave: (button) {
        setState(() => _buttons[index] = button);
      },
    );
  }

  /// Shows a bottom sheet with all preset buttons grouped by type.
  /// User picks a button to add, or taps "Custom" for manual entry.
  void _showButtonCatalogPicker(
    BuildContext context, {
    required void Function(ActionBarButton button) onPick,
  }) {
    final l10n = AppLocalizations.of(context)!;
    final catalogByType = ActionBarPresets.buttonCatalogByType;
    // Existing values in this group — to highlight already-added buttons
    final existingValues = _buttons.map((b) => b.value).toSet();

    // Type display names
    String typeLabel(ActionBarButtonType type) {
      return switch (type) {
        ActionBarButtonType.specialKey => 'Special Keys',
        ActionBarButtonType.ctrlCombo => 'Ctrl Combos',
        ActionBarButtonType.altCombo => 'Alt Combos',
        ActionBarButtonType.shiftCombo => 'Shift Combos',
        ActionBarButtonType.literal => 'Characters',
        ActionBarButtonType.modifier => 'Modifiers',
        ActionBarButtonType.action => 'Actions',
        ActionBarButtonType.confirm => 'Confirm (y/n)',
      };
    }

    // Order for display
    const typeOrder = [
      ActionBarButtonType.specialKey,
      ActionBarButtonType.ctrlCombo,
      ActionBarButtonType.modifier,
      ActionBarButtonType.confirm,
      ActionBarButtonType.literal,
      ActionBarButtonType.shiftCombo,
      ActionBarButtonType.altCombo,
      ActionBarButtonType.action,
    ];

    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      useSafeArea: true,
      builder: (ctx) {
        return DraggableScrollableSheet(
          initialChildSize: 0.7,
          minChildSize: 0.4,
          maxChildSize: 0.95,
          expand: false,
          builder: (context, scrollController) {
            return Column(
              children: [
                // Handle bar
                Container(
                  margin: const EdgeInsets.only(top: 8, bottom: 4),
                  width: 40,
                  height: 4,
                  decoration: BoxDecoration(
                    color: Theme.of(context).colorScheme.onSurfaceVariant.withValues(alpha: 0.4),
                    borderRadius: BorderRadius.circular(2),
                  ),
                ),
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                  child: Row(
                    children: [
                      Text(l10n.addButton, style: Theme.of(context).textTheme.titleMedium),
                      const Spacer(),
                      TextButton.icon(
                        icon: const Icon(Icons.edit, size: 18),
                        label: Text(l10n.customButton),
                        onPressed: () {
                          Navigator.pop(ctx);
                          _showManualButtonDialog(
                            context,
                            title: l10n.addButton,
                            onSave: onPick,
                          );
                        },
                      ),
                    ],
                  ),
                ),
                const Divider(height: 1),
                Expanded(
                  child: ListView(
                    controller: scrollController,
                    children: [
                      for (final type in typeOrder)
                        if (catalogByType.containsKey(type)) ...[
                          Padding(
                            padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
                            child: Text(
                              typeLabel(type),
                              style: Theme.of(context).textTheme.labelMedium?.copyWith(
                                    color: DesignColors.primary,
                                    fontWeight: FontWeight.w600,
                                  ),
                            ),
                          ),
                          Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 12),
                            child: Wrap(
                              spacing: 6,
                              runSpacing: 4,
                              children: [
                                for (final button in catalogByType[type]!)
                                  _CatalogChip(
                                    button: button,
                                    alreadyAdded: existingValues.contains(button.value),
                                    onTap: () {
                                      final newButton = ActionBarButton(
                                        id: 'btn_${DateTime.now().millisecondsSinceEpoch}',
                                        label: button.label,
                                        type: button.type,
                                        value: button.value,
                                        longPressValue: button.longPressValue,
                                        iconName: button.iconName,
                                        description: button.description,
                                      );
                                      onPick(newButton);
                                      Navigator.pop(ctx);
                                    },
                                  ),
                              ],
                            ),
                          ),
                        ],
                      const SizedBox(height: 16),
                    ],
                  ),
                ),
              ],
            );
          },
        );
      },
    );
  }

  /// Manual text-entry dialog for creating or editing a button.
  void _showManualButtonDialog(
    BuildContext context, {
    required String title,
    ActionBarButton? initial,
    required void Function(ActionBarButton button) onSave,
  }) {
    final labelController = TextEditingController(text: initial?.label ?? '');
    final valueController = TextEditingController(text: initial?.value ?? '');
    final longPressController =
        TextEditingController(text: initial?.longPressValue ?? '');
    final descriptionController =
        TextEditingController(text: initial?.displayDescription ?? '');
    var selectedType = initial?.type ?? ActionBarButtonType.specialKey;

    showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          title: Text(title),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: labelController,
                  decoration: InputDecoration(
                    labelText: AppLocalizations.of(context)!.buttonLabel,
                    hintText: AppLocalizations.of(context)!.buttonLabelHint,
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                  autofocus: initial == null,
                ),
                const SizedBox(height: 12),
                DropdownButtonFormField<ActionBarButtonType>(
                  initialValue: selectedType,
                  decoration: InputDecoration(
                    labelText: AppLocalizations.of(context)!.buttonType,
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                  items: ActionBarButtonType.values
                      .map((t) => DropdownMenuItem(
                            value: t,
                            child: Text(_buttonTypeLabel(t)),
                          ))
                      .toList(),
                  onChanged: (v) {
                    if (v != null) setDialogState(() => selectedType = v);
                  },
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: valueController,
                  decoration: InputDecoration(
                    labelText: AppLocalizations.of(context)!.buttonValue,
                    hintText: AppLocalizations.of(context)!.buttonValueHint,
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: longPressController,
                  decoration: InputDecoration(
                    labelText: AppLocalizations.of(context)!.buttonLongPress,
                    hintText: AppLocalizations.of(context)!.buttonLongPressHint,
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: descriptionController,
                  decoration: InputDecoration(
                    labelText: AppLocalizations.of(context)!.buttonDescription,
                    hintText: AppLocalizations.of(context)!.buttonDescriptionHint,
                    border: OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: Text(AppLocalizations.of(context)!.buttonCancel),
            ),
            FilledButton(
              onPressed: () {
                final label = labelController.text.trim();
                final value = valueController.text.trim();
                final longPress = longPressController.text.trim();
                final desc = descriptionController.text.trim();
                if (label.isNotEmpty && value.isNotEmpty) {
                  final defaultDesc =
                      ActionBarButton.defaultDescriptions[value] ?? '';
                  final customDesc =
                      desc.isEmpty || desc == defaultDesc ? null : desc;
                  final button = ActionBarButton(
                    id: initial?.id ??
                        'btn_${DateTime.now().millisecondsSinceEpoch}',
                    label: label,
                    type: selectedType,
                    value: value,
                    longPressValue: longPress.isEmpty ? null : longPress,
                    iconName: initial?.iconName,
                    description: customDesc,
                  );
                  onSave(button);
                  Navigator.pop(ctx);
                }
              },
              child: Text(initial != null ? AppLocalizations.of(context)!.buttonSave : AppLocalizations.of(context)!.add),
            ),
          ],
        ),
      ),
    );
  }
}

/// A chip widget for the button catalog picker.
class _CatalogChip extends StatelessWidget {
  final ActionBarButton button;
  final bool alreadyAdded;
  final VoidCallback onTap;

  const _CatalogChip({
    required this.button,
    required this.alreadyAdded,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final desc = button.displayDescription;
    return Tooltip(
      message: desc.isNotEmpty ? '${button.value}\n$desc' : button.value,
      child: ActionChip(
        avatar: alreadyAdded
            ? Icon(Icons.check, size: 16, color: DesignColors.primary)
            : null,
        label: Text(
          button.label,
          style: TextStyle(
            fontSize: 13,
            color: alreadyAdded
                ? Theme.of(context).colorScheme.onSurfaceVariant
                : null,
          ),
        ),
        backgroundColor: alreadyAdded
            ? Theme.of(context).colorScheme.surfaceContainerHighest
            : null,
        onPressed: onTap,
      ),
    );
  }
}
