package com.jenew.zroksmoke;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.os.Bundle;
import android.content.pm.PackageManager;
import android.os.Handler;
import android.os.Looper;
import android.text.InputType;
import android.view.View;
import android.widget.AdapterView;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.Spinner;
import android.widget.TextView;
import org.json.JSONArray;
import org.json.JSONObject;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class MainActivity extends Activity {
    private final ExecutorService worker = Executors.newSingleThreadExecutor();
    private final Handler main = new Handler(Looper.getMainLooper());
    private LinearLayout rootLayout;
    private TextView statusView, messageView;
    private LinearLayout servicesContainer;
    private EditText endpointInput, tokenInput, hostInput, targetInput, advancedOptionsInput, accessShareInput, accessTokenInput, accessBindInput, driveShareInput, driveRemoteInput, driveLocalInput, driveDestinationInput, namespaceInput, metricsShareInput, metricsDurationInput;
    private Spinner backendInput, modeInput, managedShareInput;
    private final List<String> managedShareTokens = new ArrayList<>();
    private static final String DEFAULT_API = "https://api-v2.zrok.io";
    private static final String[] BACKENDS = {
        "tcpTunnel", "web", "proxy", "udpTunnel", "caddy", "drive", "socks"
    };
    private static final String[] MODES = {"private", "public"};

    @Override protected void onCreate(Bundle state) {
        super.onCreate(state);
        go.Seq.setContext(getApplicationContext());
        try {
            bridge.Bridge.initialize(getFilesDir().getAbsolutePath() + "/zrok2");
            new java.io.File(getFilesDir(), "public").mkdirs();
            new java.io.File(getFilesDir(), "drive").mkdirs();
            new java.io.File(getFilesDir(), "drive-files").mkdirs();
        } catch (Throwable error) {
            showFatal("Could not initialize private zrok storage: " + safeMessage(error));
            return;
        }
        buildScreen();
        if (android.os.Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 7402);
        }
        refresh();
        if (bridge.Bridge.autoStartCount() > 0) ZrokForegroundService.start(this);
    }

    private void buildScreen() {
        ScrollView scroll = new ScrollView(this);
        rootLayout = new LinearLayout(this);
        rootLayout.setOrientation(LinearLayout.VERTICAL);
        rootLayout.setPadding(dp(20), dp(24), dp(20), dp(28));
        scroll.addView(rootLayout);
        setContentView(scroll);

        TextView title = new TextView(this);
        title.setText("JENEW • ZROK CONTROL");
        title.setTextSize(23);
        title.setPadding(0, 0, 0, dp(8));
        rootLayout.addView(title);

        statusView = new TextView(this);
        statusView.setTextSize(15);
        statusView.setPadding(0, dp(8), 0, dp(12));
        rootLayout.addView(statusView);

        messageView = new TextView(this);
        messageView.setTextSize(13);
        messageView.setPadding(0, 0, 0, dp(12));
        rootLayout.addView(messageView);

        addSection("ENVIRONMENT");
        endpointInput = addInput("zrok API endpoint", DEFAULT_API, false);
        tokenInput = addInput("Account token (used only to enable)", "", true);
        tokenInput.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        hostInput = addInput("Environment host", "jenew-android", false);
        addButton("ENABLE ENVIRONMENT", () -> {
            final String endpoint = value(endpointInput);
            final String token = value(tokenInput);
            final String host = value(hostInput);
            tokenInput.setText("");
            if (token.isEmpty()) {
                messageView.setText("Enter your zrok account token first.");
                return;
            }
            runTask("Enabling zrok environment…", () -> {
                bridge.Bridge.enableEnvironment(endpoint, token, host, "JENEW Android zrok control");
                return null;
            });
        });

        addSection("CREATE SHARE");
        backendInput = addSpinner("Backend type", BACKENDS);
        modeInput = addSpinner("Share visibility", MODES);
        targetInput = addInput("Backend target", "127.0.0.1:8080", false);
        advancedOptionsInput = addInput("Advanced share options (JSON)", "{\"backendMode\":\"tcpTunnel\",\"shareMode\":\"private\",\"target\":\"127.0.0.1:8080\",\"permissionMode\":\"open\"}", false);
        advancedOptionsInput.setSingleLine(false);
        advancedOptionsInput.setMinLines(3);
        advancedOptionsInput.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_MULTI_LINE | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);
        addButton("CREATE SHARE WITH ADVANCED OPTIONS", () -> {
            final String options = value(advancedOptionsInput);
            runTask("Creating advanced zrok share…", () -> {
                bridge.Bridge.createShareWithOptions(options);
                return null;
            });
        });

        addSection("PRIVATE ACCESS");
        managedShareInput = addSpinner("Select managed share (share tokens stay hidden)", new String[]{"No managed shares yet"});
        accessBindInput = addInput("Local TCP bind address", "127.0.0.1:9000", false);
        addButton("START PRIVATE TCP ACCESS", () -> {
            final String share = selectedManagedShareToken();
            final String bind = value(accessBindInput);
            runTask("Starting private TCP access…", () -> {
                ZrokForegroundService.start(this);
                try {
                    bridge.Bridge.startTCPAccessForwarder(share, bind);
                } catch (Throwable failure) {
                    if (bridge.Bridge.activeBackendCount() == 0) ZrokForegroundService.stop(this);
                    throw failure;
                }
                return null;
            });
        });
        addButton("STOP PRIVATE TCP ACCESS", () -> runTask("Stopping private TCP access…", () -> {
            bridge.Bridge.stopTCPAccessForwarder(selectedManagedShareToken());
            if (bridge.Bridge.activeBackendCount() == 0) ZrokForegroundService.stop(this);
            return null;
        }));
        addButton("CREATE PRIVATE ACCESS GRANT", () -> {
            final String share = selectedManagedShareToken();
            runTask("Creating private access grant…", () -> {
                bridge.Bridge.createAccessGrant(share, "");
                return null; // Never render the access token in the UI result panel.
            });
        });
        accessTokenInput = addInput("Access token to revoke", "", true);
        accessTokenInput.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        addButton("REVOKE PRIVATE ACCESS GRANT", () -> {
            final String access = value(accessTokenInput);
            confirm("Revoke private access?", "This revokes the selected access grant at the zrok controller.", () ->
                runTask("Revoking private access…", () -> {
                    bridge.Bridge.deleteAccessGrant(access);
                    accessTokenInput.setText("");
                    return null;
                }));
        });
        addButton("REFRESH PRIVATE ACCESS LIST", () -> runTask(
            "Refreshing private access records…", () -> {
                bridge.Bridge.listManagedAccesses(); // Contains revocation tokens; intentionally never displayed.
                return null;
            }));

        addSection("ZROK DRIVE FILE OPERATIONS");
        driveRemoteInput = addInput("Remote Drive path", "/", false);
        driveLocalInput = addInput("Local app-private path (relative)", "downloads/file.bin", false);
        driveDestinationInput = addInput("Remote destination path", "/backup/file.bin", false);
        addButton("LIST DRIVE DIRECTORY", () -> runTaskWithResult("Listing remote Drive files…",
            () -> bridge.Bridge.driveList(selectedManagedShareToken(), value(driveRemoteInput), false)));
        addButton("DOWNLOAD FROM DRIVE", () -> runTask("Downloading from Drive…", () -> {
            bridge.Bridge.driveDownload(selectedManagedShareToken(), value(driveRemoteInput), value(driveLocalInput));
            return null;
        }));
        addButton("UPLOAD TO DRIVE", () -> runTask("Uploading to Drive…", () -> {
            bridge.Bridge.driveUpload(selectedManagedShareToken(), value(driveLocalInput), value(driveRemoteInput));
            return null;
        }));
        addButton("CREATE DRIVE DIRECTORY", () -> runTask("Creating remote Drive directory…", () -> {
            bridge.Bridge.driveMkdir(selectedManagedShareToken(), value(driveRemoteInput));
            return null;
        }));
        addButton("COPY DRIVE ITEM", () -> runTask("Copying remote Drive item…", () -> {
            bridge.Bridge.driveCopy(selectedManagedShareToken(), value(driveRemoteInput), value(driveDestinationInput), true);
            return null;
        }));
        addButton("MOVE DRIVE ITEM", () -> runTask("Moving remote Drive item…", () -> {
            bridge.Bridge.driveMove(selectedManagedShareToken(), value(driveRemoteInput), value(driveDestinationInput), true);
            return null;
        }));
        addButton("DELETE DRIVE ITEM", () -> confirm("Delete Drive item?", "This permanently removes the selected remote file or directory.", () ->
            runTask("Deleting remote Drive item…", () -> {
                bridge.Bridge.driveRemove(selectedManagedShareToken(), value(driveRemoteInput));
                return null;
            })));
        addButton("CREATE ZROK SHARE", () -> {
            final String backend = backendInput.getSelectedItem().toString();
            final String mode = modeInput.getSelectedItem().toString();
            final String target = value(targetInput);
            runTask("Creating " + mode + " " + backend + " share…", () -> {
                bridge.Bridge.createShare(backend, mode, target);
                return null;
            });
        });

        addSection("NAMESPACES, RESERVED NAMES & METRICS");
        addButton("LIST AVAILABLE NAMESPACES", () -> runTaskWithResult("Loading namespaces…",
            () -> bridge.Bridge.listShareNamespaces()));
        namespaceInput = addInput("Namespace token", "", false);
        addButton("LIST NAMES IN NAMESPACE", () -> runTaskWithResult("Loading reserved names…",
            () -> bridge.Bridge.listNamesForNamespace(value(namespaceInput))));
        addButton("LIST ALL NAMED SHARES", () -> runTaskWithResult("Loading named shares…",
            () -> bridge.Bridge.listAllNames()));
        metricsDurationInput = addInput("Metrics duration (e.g. 24h, 7d)", "24h", false);
        addButton("GET ACCOUNT METRICS", () -> runTaskWithResult("Loading account usage metrics…",
            () -> bridge.Bridge.getAccountMetrics(value(metricsDurationInput))));
        addButton("GET SHARE METRICS", () -> runTaskWithResult("Loading share metrics…",
            () -> bridge.Bridge.getShareMetrics(selectedManagedShareToken(), value(metricsDurationInput))));

        addSection("MANAGED SERVICES");
        addButton("REFRESH STATUS + SERVICES", this::refresh);
        servicesContainer = new LinearLayout(this);
        servicesContainer.setOrientation(LinearLayout.VERTICAL);
        rootLayout.addView(servicesContainer);
        addButton("DISABLE ENVIRONMENT", () -> confirm(
            "Disable zrok environment?",
            "This removes the local zrok identity and credentials after the controller confirms the disable. Stop all forwarders first.",
            () -> runTask("Disabling environment…", () -> {
                bridge.Bridge.disableEnvironment();
                return null;
            })
        ));
    }

    private EditText addInput(String hint, String initial, boolean password) {
        EditText input = new EditText(this);
        input.setSingleLine(true);
        input.setHint(hint);
        input.setText(initial);
        input.setTextSize(14);
        rootLayout.addView(input, new LinearLayout.LayoutParams(-1, dp(52)));
        return input;
    }

    private Spinner addSpinner(String label, String[] values) {
        TextView caption = new TextView(this);
        caption.setText(label);
        caption.setTextSize(13);
        caption.setPadding(0, dp(8), 0, 0);
        rootLayout.addView(caption);
        Spinner spinner = new Spinner(this);
        ArrayAdapter<String> adapter = new ArrayAdapter<>(
            this, android.R.layout.simple_spinner_item, values);
        adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);
        spinner.setAdapter(adapter);
        rootLayout.addView(spinner, new LinearLayout.LayoutParams(-1, dp(48)));
        return spinner;
    }

    private void addSection(String label) {
        TextView text = new TextView(this);
        text.setText(label);
        text.setTextSize(12);
        text.setPadding(0, dp(20), 0, dp(6));
        rootLayout.addView(text);
    }

    private void addButton(String label, Runnable action) {
        Button button = new Button(this);
        button.setText(label);
        button.setOnClickListener(v -> action.run());
        rootLayout.addView(button, new LinearLayout.LayoutParams(-1, dp(52)));
    }

    private void refresh() {
        messageView.setText("Refreshing…");
        worker.execute(() -> {
            try {
                String status = bridge.Bridge.status();
                String shares = bridge.Bridge.listManagedShares();
                main.post(() -> renderState(status, shares));
            } catch (Throwable error) {
                main.post(() -> messageView.setText("Refresh failed: " + safeMessage(error)));
            }
        });
    }

    private void renderState(String statusJson, String sharesJson) {
        try {
            JSONObject status = new JSONObject(statusJson);
            statusView.setText("Initialized: " + status.optBoolean("initialized")
                + "   •   Enabled: " + status.optBoolean("enabled")
                + "   •   Identity saved: " + status.optBoolean("identitySaved")
                + "   •   Active forwarders: " + status.optInt("activeForwarders")
                + "   •   Share engines: " + status.optInt("activeShareBackends")
                + "   •   Private access: " + status.optInt("activeAccessForwarders")
                + "   •   In-flight tests: " + status.optInt("activeOperations")
                + "   •   Managed: " + status.optInt("managedShares")
                + (status.has("apiEndpoint") ? "\nAPI: " + status.optString("apiEndpoint") : "")
                + (status.optString("lastRuntimeError", "").isEmpty() ? "" : "\nRuntime error: " + status.optString("lastRuntimeError")));
            JSONArray shares = new JSONArray(sharesJson);
            updateManagedShareSelector(shares);
            servicesContainer.removeAllViews();
            for (int i = 0; i < shares.length(); i++) addServiceRow(shares.getJSONObject(i));
            messageView.setText("State refreshed. Credentials and share tokens are kept inside app-private storage.");
        } catch (Throwable error) {
            messageView.setText("Could not render service list: " + safeMessage(error));
        }
    }

    private void updateManagedShareSelector(JSONArray shares) throws Exception {
        String previouslySelected = selectedManagedShareTokenOrEmpty();
        managedShareTokens.clear();
        List<String> labels = new ArrayList<>();
        for (int i = 0; i < shares.length(); i++) {
            JSONObject share = shares.getJSONObject(i);
            String token = share.optString("shareToken", "").trim();
            if (token.isEmpty()) continue;
            managedShareTokens.add(token);
            String backend = share.optString("backendMode", "share");
            String mode = share.optString("shareMode", "");
            String target = share.optString("target", "");
            labels.add(backend + " / " + mode + " — " + target);
        }
        if (labels.isEmpty()) labels.add("No managed shares yet — create a share first");
        ArrayAdapter<String> adapter = new ArrayAdapter<>(this, android.R.layout.simple_spinner_item, labels);
        adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);
        managedShareInput.setAdapter(adapter);
        int selected = managedShareTokens.indexOf(previouslySelected);
        managedShareInput.setSelection(selected >= 0 ? selected : 0);
    }

    private String selectedManagedShareTokenOrEmpty() {
        int position = managedShareInput == null ? -1 : managedShareInput.getSelectedItemPosition();
        return position >= 0 && position < managedShareTokens.size() ? managedShareTokens.get(position) : "";
    }

    private String selectedManagedShareToken() {
        String token = selectedManagedShareTokenOrEmpty();
        if (token.isEmpty()) throw new IllegalStateException("Select a managed share first. Create a share, then refresh the managed-share list.");
        return token;
    }

    private void addServiceRow(JSONObject share) {
        final String shareToken = share.optString("shareToken");
        final String target = share.optString("target");
        final String backend = share.optString("backendMode");
        final String mode = share.optString("shareMode");
        final boolean running = share.optBoolean("running");
        JSONArray endpointArray = share.optJSONArray("frontendEndpoints");
        final List<String> endpoints = new ArrayList<>();
        if (endpointArray != null) {
            for (int i = 0; i < endpointArray.length(); i++) {
                String endpoint = endpointArray.optString(i);
                if (endpoint != null && !endpoint.isEmpty()) endpoints.add(endpoint);
            }
        }

        TextView info = new TextView(this);
        StringBuilder details = new StringBuilder();
        details.append((running ? "● RUNNING" : "○ STOPPED"))
            .append("  ").append(backend).append(" / ").append(mode)
            .append("\nTarget: ").append(target);
        if (!endpoints.isEmpty()) details.append("\nFrontend: ").append(endpoints.get(0));
        info.setText(details.toString());
        info.setTextSize(14);
        info.setPadding(0, dp(14), 0, dp(4));
        servicesContainer.addView(info);

        if (!endpoints.isEmpty()) {
            Button copyEndpoint = new Button(this);
            copyEndpoint.setText("COPY FRONTEND ENDPOINT");
            copyEndpoint.setOnClickListener(v -> {
                ClipboardManager clipboard = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
                clipboard.setPrimaryClip(ClipData.newPlainText("zrok frontend endpoint", endpoints.get(0)));
                messageView.setText("Frontend endpoint copied to clipboard.");
            });
            servicesContainer.addView(copyEndpoint, new LinearLayout.LayoutParams(-1, dp(46)));
        }

        Button engineToggle = new Button(this);
        engineToggle.setText(running ? "STOP " + backend.toUpperCase() + " ENGINE" : "START " + backend.toUpperCase() + " ENGINE");
        engineToggle.setOnClickListener(v -> runTask(running ? "Stopping " + backend + " engine…" : "Starting " + backend + " engine…", () -> {
            if (running) {
                bridge.Bridge.stopShareBackend(shareToken);
                if (bridge.Bridge.activeBackendCount() == 0) ZrokForegroundService.stop(this);
            } else {
                ZrokForegroundService.start(this);
                try {
                    bridge.Bridge.startShareBackend(shareToken, backend, target);
                } catch (Throwable failure) {
                    if (bridge.Bridge.activeBackendCount() == 0) ZrokForegroundService.stop(this);
                    throw failure;
                }
            }
            return null;
        }));
        servicesContainer.addView(engineToggle, new LinearLayout.LayoutParams(-1, dp(48)));

        if ("tcpTunnel".equals(backend) && "private".equals(mode)) {
            Button test = new Button(this);
            test.setText("TEST PRIVATE SHARE");
            test.setOnClickListener(v -> runTaskWithResult("Testing private share traffic…",
                () -> bridge.Bridge.testPrivateShare(shareToken)));
            servicesContainer.addView(test, new LinearLayout.LayoutParams(-1, dp(48)));
        }

        Button delete = new Button(this);
        delete.setText("DELETE SHARE");
        delete.setOnClickListener(v -> confirm(
            "Delete this zrok share?",
            "This removes the share from the zrok account. Stop its forwarder first if it is running.",
            () -> runTask("Deleting share…", () -> {
                bridge.Bridge.deleteShare(shareToken);
                return null;
            })
        ));
        servicesContainer.addView(delete, new LinearLayout.LayoutParams(-1, dp(48)));
    }

    private void confirm(String title, String message, Runnable accepted) {
        new AlertDialog.Builder(this)
            .setTitle(title)
            .setMessage(message)
            .setNegativeButton("CANCEL", (dialog, which) -> dialog.dismiss())
            .setPositiveButton("CONTINUE", (dialog, which) -> accepted.run())
            .show();
    }

    private interface Work { Object run() throws Exception; }

    private void runTask(String label, Work work) {
        messageView.setText(label);
        worker.execute(() -> {
            try {
                work.run();
                String status = bridge.Bridge.status();
                String shares = bridge.Bridge.listManagedShares();
                main.post(() -> renderState(status, shares));
            } catch (Throwable error) {
                final String failure = "Operation failed: " + safeMessage(error);
                try {
                    String status = bridge.Bridge.status();
                    String shares = bridge.Bridge.listManagedShares();
                    main.post(() -> { renderState(status, shares); messageView.setText(failure); });
                } catch (Throwable refreshError) {
                    main.post(() -> messageView.setText(failure + "\nStatus refresh also failed: " + safeMessage(refreshError)));
                }
            }
        });
    }

    private void runTaskWithResult(String label, Work work) {
        messageView.setText(label);
        worker.execute(() -> {
            try {
                Object result = work.run();
                String status = bridge.Bridge.status();
                String shares = bridge.Bridge.listManagedShares();
                main.post(() -> {
                    renderState(status, shares);
                    if (result != null) messageView.setText("ZROK OPERATION RESULT\n" + result);
                });
            } catch (Throwable error) {
                final String failure = "Operation failed: " + safeMessage(error);
                try {
                    String status = bridge.Bridge.status();
                    String shares = bridge.Bridge.listManagedShares();
                    main.post(() -> { renderState(status, shares); messageView.setText(failure); });
                } catch (Throwable refreshError) {
                    main.post(() -> messageView.setText(failure + "\nStatus refresh also failed: " + safeMessage(refreshError)));
                }
            }
        });
    }

    private String value(EditText input) { return input.getText().toString().trim(); }
    private int dp(int value) { return (int) (value * getResources().getDisplayMetrics().density + 0.5f); }
    private String safeMessage(Throwable error) {
        String message = error.getMessage();
        return message == null ? error.getClass().getSimpleName() : message;
    }

    private void showFatal(String message) {
        TextView view = new TextView(this);
        view.setPadding(dp(24), dp(32), dp(24), dp(24));
        view.setText(message);
        setContentView(view);
    }

    @Override protected void onDestroy() {
        worker.shutdownNow();
        super.onDestroy();
    }
}
