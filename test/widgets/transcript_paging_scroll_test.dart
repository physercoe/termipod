import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:termipod/screens/terminal/widgets/ansi_text_view.dart';
import 'package:termipod/screens/terminal/widgets/transcript_paging_scroll.dart';

void main() {
  late List<bool> pages;
  late ScrollController controller;
  setUp(() {
    pages = [];
    controller = ScrollController();
  });
  tearDown(() => controller.dispose());

  testWidgets(
    'real terminal view forwards pulls, but selection mode does not',
    (tester) async {
      SharedPreferences.setMockInitialValues({});
      Future<void> mountTerminal(TerminalMode mode) async {
        await tester.pumpWidget(
          ProviderScope(
            child: MaterialApp(
              home: Scaffold(
                body: SizedBox(
                  height: 400,
                  child: AnsiTextView(
                    text: List.generate(10, (i) => 'message $i').join('\n'),
                    paneWidth: 80,
                    paneHeight: 10,
                    isFullscreen: true,
                    mode: mode,
                    onTranscriptPage: pages.add,
                  ),
                ),
              ),
            ),
          ),
        );
        // The terminal cursor blinks continuously, so do not pumpAndSettle.
        await tester.pump(const Duration(milliseconds: 300));
      }

      await mountTerminal(TerminalMode.normal);
      await tester.drag(find.byType(ListView), const Offset(0, 160));
      await tester.pump(const Duration(milliseconds: 300));
      expect(pages, [true]);
      await mountTerminal(TerminalMode.scroll);
      await tester.drag(find.byType(ListView), const Offset(0, 160));
      await tester.pump(const Duration(milliseconds: 300));
      expect(pages, [true]);
    },
  );

  Future<void> mount(
    WidgetTester tester, {
    int rows = 5,
    bool enabled = true,
  }) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: Center(
            child: SizedBox(
              width: 300,
              height: 300,
              child: TranscriptPagingScroll(
                onPage: enabled ? pages.add : null,
                child: ListView.builder(
                  controller: controller,
                  physics: const AlwaysScrollableScrollPhysics(
                    parent: ClampingScrollPhysics(),
                  ),
                  itemExtent: 20,
                  itemCount: rows,
                  itemBuilder: (_, i) => Text('row $i'),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  testWidgets('short viewport pages older and newer with direct edge pulls', (
    tester,
  ) async {
    await mount(tester);
    await tester.drag(find.byType(ListView), const Offset(0, 150));
    await tester.pumpAndSettle();
    expect(pages, [true]);
    await tester.drag(find.byType(ListView), const Offset(0, -150));
    await tester.pumpAndSettle();
    expect(pages, [true, false]);
  });

  testWidgets('long drag sends only one page and ignores its fling', (
    tester,
  ) async {
    await mount(tester);
    final gesture = await tester.startGesture(
      tester.getCenter(find.byType(ListView)),
    );
    for (var i = 0; i < 6; i++) {
      await gesture.moveBy(const Offset(0, 50));
      await tester.pump(const Duration(milliseconds: 20));
    }
    await gesture.up();
    await tester.pumpAndSettle();
    expect(pages, [true]);
  });

  testWidgets('local pane scrolling and programmatic jumps do not page', (
    tester,
  ) async {
    await mount(tester, rows: 100);
    controller.jumpTo(400);
    await tester.pump();
    await tester.drag(find.byType(ListView), const Offset(0, -100));
    await tester.pumpAndSettle();
    expect(controller.offset, greaterThan(400));
    expect(pages, isEmpty);
  });

  testWidgets('pinch or pane-switch gestures do not send paging keys', (
    tester,
  ) async {
    await mount(tester);
    final center = tester.getCenter(find.byType(ListView));
    final first = await tester.startGesture(
      center - const Offset(20, 0),
      pointer: 1,
    );
    final second = await tester.startGesture(
      center + const Offset(20, 0),
      pointer: 2,
    );
    await first.moveBy(const Offset(0, 150));
    await second.up();
    await first.moveBy(const Offset(0, 80));
    await first.up();
    await tester.pumpAndSettle();
    expect(pages, isEmpty);
  });

  testWidgets('disabled paging leaves ordinary terminal scrolling unchanged', (
    tester,
  ) async {
    await mount(tester, enabled: false);
    await tester.drag(find.byType(ListView), const Offset(0, 150));
    await tester.pumpAndSettle();
    expect(pages, isEmpty);
  });
}
