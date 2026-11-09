package com.jenew.zroksmoke;

import android.app.Activity;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.InputType;
import android.view.View;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import org.json.JSONArray;
import org.json.JSONObject;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class MainActivity extends Activity {
    private final ExecutorService worker = Executors.newSingleThreadExecutor();
    private final Handler main = new Handler(Looper.getMainLooper());
    private LinearLayout rootLayout;
    private TextView statusView;
    private TextView messageView;
    private LinearLayout servicesContainer;
    private EditText endpointInput, tokenInput, hostInput, targetInput;
    private static final String DEFAULT_API = "https://api-v2.zrok.io";

    @Override protected void onCreate(Bundle state) {
        super.onCreate(state);
        go.Seq.setContext(getApplicationContext());
        try {
            bridge.Bridge.initialize(getFilesDir().getAbsolutePath() + "/zrok2");
        } catch (Throwable error) {
            showFatal("Could not initialize private zrok storage: " + safeMessage(error));
            return;
        }
        buildScreen();
        refresh();
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
            runTask("Enabling zrok environment…", () ->
                bridge.Bridge.enableEnvironment(endpoint, token, host, "JENEW Android zrok control"));
        });

        addSection("CREATE TCP SERVICE");
        targetInput = addInput("Local target (host:port)", "127.0.0.1:8080", false);
        addButton("CREATE PRIVATE TCP SHARE", () -> {
            final String target = value(targetInput);
            runTask("Creating private TCP share…", () -> {
                bridge.Bridge.createShare("tcpTunnel", "private", target);
                return null;
            });
        });

        addSection("MANAGED SERVICES");
        addButton("REFRESH STATUS + SERVICES", this::refresh);
        servicesContainer = new LinearLayout(this);
        servicesContainer.setOrientation(LinearLayout.VERTICAL);
        rootLayout.addView(servicesContainer);
        addButton("DISABLE ENVIRONMENT", () ->
            runTask("Disabling environment…", () -> bridge.Bridge.disableEnvironment()));
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
                + "   •   Active forwarders: " + status.optInt("activeForwarders")
                + "   •   Managed: " + status.optInt("managedShares"));
            JSONArray shares = new JSONArray(sharesJson);
            servicesContainer.removeAllViews();
            for (int i = 0; i < shares.length(); i++) {
                JSONObject share = shares.getJSONObject(i);
                addServiceRow(share);
            }
            messageView.setText("State refreshed. Share tokens remain inside the app UI and private registry.");
        } catch (Throwable error) {
            messageView.setText("Could not render service list: " + safeMessage(error));
        }
    }

    private View findSection(String label) {
        for (int i = 0; i < rootLayout.getChildCount(); i++) {
            View view = rootLayout.getChildAt(i);
            if (view instanceof TextView && label.contentEquals(((TextView) view).getText())) return view;
        }
        return null;
    }

    private void addServiceRow(JSONObject share) {
        final String shareToken = share.optString("shareToken");
        final String target = share.optString("target");
        final boolean running = share.optBoolean("running");
        TextView info = new TextView(this);
        info.setText((running ? "● RUNNING" : "○ STOPPED") + "  " + target
            + "\n" + share.optString("backendMode") + " / " + share.optString("shareMode"));
        info.setTextSize(14);
        info.setPadding(0, dp(12), 0, dp(4));
        servicesContainer.addView(info);
        Button toggle = new Button(this);
        toggle.setText(running ? "STOP FORWARDER" : "START FORWARDER");
        toggle.setOnClickListener(v -> runTask(running ? "Stopping…" : "Starting…", () -> {
            if (running) bridge.Bridge.stopTCPForwarder(shareToken);
            else bridge.Bridge.startTCPForwarder(shareToken, target);
            return null;
        }));
        servicesContainer.addView(toggle, new LinearLayout.LayoutParams(-1, dp(48)));
        Button delete = new Button(this);
        delete.setText("DELETE SHARE");
        delete.setOnClickListener(v -> runTask("Deleting share…", () -> {
            bridge.Bridge.deleteShare(shareToken);
            return null;
        }));
        servicesContainer.addView(delete, new LinearLayout.LayoutParams(-1, dp(48)));
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
                main.post(() -> messageView.setText("Operation failed: " + safeMessage(error)));
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
