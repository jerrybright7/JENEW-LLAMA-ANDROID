package com.jenew.zroksmoke;

import android.app.Activity;
import android.os.Bundle;
import android.util.Log;
import android.widget.TextView;

public final class MainActivity extends Activity {
    private static final String TAG = "ZrokSmoke";

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        TextView resultView = new TextView(this);
        resultView.setPadding(32, 48, 32, 32);
        resultView.setTextSize(18);
        resultView.setText("Starting isolated zrok Go/JNI smoke test...");
        setContentView(resultView);

        try {
            // Go Mobile requires the Android context to be supplied before SDK calls.
            go.Seq.setContext(getApplicationContext());
            bridge.Bridge.touch();
            String result = bridge.Bridge.sdkCompileProbe();

            if (result == null || !result.contains("zrok SDK v2 bridge ready")) {
                throw new IllegalStateException("Unexpected bridge response: " + result);
            }

            Log.i(TAG, "ZROK_SMOKE_PASS: " + result);
            resultView.setText("PASS\n\n" + result + "\n\nGo JNI loaded and native bridge call returned.");
        } catch (Throwable error) {
            Log.e(TAG, "ZROK_SMOKE_FAIL", error);
            resultView.setText("FAIL\n\n" + error.getClass().getName() + ": " + error.getMessage());
        }
    }
}
